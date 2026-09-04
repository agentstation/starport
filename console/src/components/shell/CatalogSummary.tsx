import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSyncExternalStore } from "react";

import {
  ApiError,
  cancelCatalogRefresh,
  onCredentialChange,
  sessionIdentity,
  startCatalogRefresh,
  type CatalogAdminStatus,
  type CatalogOperation,
  type CatalogSummary,
} from "@/lib/api";
import { OPERATION_INTERVAL, STATUS_INTERVAL, SUMMARY_INTERVAL, queries } from "@/lib/queries";

// CatalogSummary owns the whole read lifecycle of the catalog surface: one
// summary query for the shell, one operator status query for the panel, and
// the two refresh actions. Every rule that decides when a request leaves this
// browser is a pure function here, so a test states the rule instead of
// waiting for a timer.
//
// Three rules shape the cadence. A reader who is not looking at the page draws
// no request. A gateway that answered 503 with a Retry-After sets the next
// wait itself. A gateway that refused the credential stops the reads until the
// session behind them changes.

// RETRY_AFTER_MIN_SECONDS and RETRY_AFTER_MAX_SECONDS bound what this browser
// honors. A gateway that asks for less than the minimum still gets a pause. A
// gateway that asks for more than the maximum does not stop the console for
// the rest of the day.
export const RETRY_AFTER_MIN_SECONDS = 5;
export const RETRY_AFTER_MAX_SECONDS = 300;

// RETRY_AFTER_DEFAULT_SECONDS is the wait after a 503 that carried no
// Retry-After header, or one this browser could not read.
export const RETRY_AFTER_DEFAULT_SECONDS = 30;

// UNAUTHORIZED_SENTENCE is what a reader sees when the gateway refuses the
// catalog read. It states the limit of the session and names no route, no
// scope, and no failure.
export const UNAUTHORIZED_SENTENCE = "This session cannot read the catalog.";

// ADMIN_ONLY_SENTENCE is what a models:read reader sees in place of the
// operator sections. It is a statement of scope, not an error.
export const ADMIN_ONLY_SENTENCE = "Source, schedule, and provider detail need an admin session.";

// refusedRead reports that the gateway refused the credential this browser
// presented. A refusal is not a failure to retry: the same credential draws
// the same answer, so the read stops until the session changes.
export function refusedRead(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 401 || error.status === 403);
}

// retryAfterMilliseconds reads the wait a 503 asked for. A gateway with no
// catalog answers 503 with a Retry-After header, and this browser waits that
// long instead of polling at its own cadence. Any other error, and any other
// status, reads as undefined.
export function retryAfterMilliseconds(error: unknown): number | undefined {
  if (!(error instanceof ApiError) || error.status !== 503) return undefined;
  const asked = error.retryAfter;
  const seconds =
    asked === undefined || !Number.isFinite(asked) ? RETRY_AFTER_DEFAULT_SECONDS : asked;
  const bounded = Math.min(Math.max(seconds, RETRY_AFTER_MIN_SECONDS), RETRY_AFTER_MAX_SECONDS);
  return bounded * 1000;
}

// summaryCadence is the whole interval rule of the shell summary. A refused
// read stops. A 503 waits the interval the gateway asked for. Every other
// state reads at the normal cadence.
export function summaryCadence(error: unknown): number | false {
  if (refusedRead(error)) return false;
  return retryAfterMilliseconds(error) ?? SUMMARY_INTERVAL;
}

// statusCadence is the interval rule of the operator status. A closed panel
// polls nothing, because no reader looks at what it holds. An open panel with
// a run in flight follows the run. An open panel without one reads at the
// slower cadence.
export function statusCadence(open: boolean, working: boolean, error: unknown): number | false {
  if (!open || refusedRead(error)) return false;
  return working ? OPERATION_INTERVAL : STATUS_INTERVAL;
}

// openOperation finds the run in flight, if the gateway reports one. A run is
// open while the gateway holds it accepted or running.
export function openOperation(
  status: CatalogAdminStatus | undefined,
): CatalogOperation | undefined {
  return status?.operations?.find(
    (operation) => operation.state === "accepted" || operation.state === "running",
  );
}

// useSessionIdentity re-reads the credential identity whenever this browser
// changes it. The identity sits inside the query keys, so a new console
// session, a released session, a pasted key, and a rotated key each start a
// new record and send one new request, while the refused record stays
// stopped.
export function useSessionIdentity(): string {
  return useSyncExternalStore(onCredentialChange, sessionIdentity, () => "none");
}

// CatalogSummaryRead is what the shell learns from the safe route.
export type CatalogSummaryRead = {
  summary: CatalogSummary | undefined;
  error: unknown;
  pending: boolean;
  // refused reports that the gateway does not serve the catalog to this
  // session. The chip states the limit in place of a freshness.
  refused: boolean;
  session: string;
};

// useCatalogSummary reads the one catalog summary the console holds. The shell
// calls it once. Every other surface reads the same record through the same
// key, so the console never opens a second summary read.
export function useCatalogSummary(): CatalogSummaryRead {
  const session = useSessionIdentity();
  const query = useQuery({
    ...queries.catalogSummary(session),
    retry: false,
    // A reader who is not looking draws no request. This is the default of the
    // library, and it is stated here because the cadence rule depends on it.
    refetchIntervalInBackground: false,
    refetchInterval: (record) => summaryCadence(record.state.error),
    refetchOnWindowFocus: (record) => !refusedRead(record.state.error),
    refetchOnReconnect: (record) => !refusedRead(record.state.error),
  });
  return {
    summary: query.data,
    error: query.error,
    pending: query.isPending,
    refused: refusedRead(query.error),
    session,
  };
}

// CatalogAdminRead is what the panel learns from the operator route.
export type CatalogAdminRead = {
  status: CatalogAdminStatus | undefined;
  // admin reports that this session reads the operator route. The chip adds
  // its operator elements only when this is true.
  admin: boolean;
  working: CatalogOperation | undefined;
};

// useCatalogAdminStatus reads the operator status. It sends one request when
// the summary first answers, one more after each new generation, and then
// polls only while the panel is open. A session the summary route refused
// sends none at all, so a refused reader draws exactly one catalog request.
export function useCatalogAdminStatus(read: CatalogSummaryRead, open: boolean): CatalogAdminRead {
  const generation = read.summary?.generation_id ?? "";
  const query = useQuery({
    ...queries.catalogStatus(read.session, generation),
    enabled: !read.refused && read.summary !== undefined,
    retry: false,
    refetchIntervalInBackground: false,
    refetchInterval: (record) =>
      statusCadence(open, openOperation(record.state.data) !== undefined, record.state.error),
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  return {
    status: query.data,
    admin: query.data !== undefined,
    working: openOperation(query.data),
  };
}

// useCatalogRefresh accepts one refresh run and ends one open run. Both
// actions read the operator status again as soon as the gateway answers, so
// the panel states the new run without a wait for the next poll.
export function useCatalogRefresh() {
  const client = useQueryClient();
  const reread = () => {
    void client.invalidateQueries({ queryKey: ["catalog-status"] });
    void client.invalidateQueries({ queryKey: ["catalog-summary"] });
  };
  const start = useMutation({ mutationFn: () => startCatalogRefresh(), onSettled: reread });
  const cancel = useMutation({
    mutationFn: (runID: string) => cancelCatalogRefresh(runID),
    onSettled: reread,
  });
  return { start, cancel };
}
