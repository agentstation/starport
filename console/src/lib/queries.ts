import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";

import * as api from "./api";
import type { ActivityFilters, ActivityPage } from "./api";

// queries owns every query key and fetcher the console reads. A route or a
// component spreads one factory into useQuery and adds only what the call
// site knows: whether the gateway is reachable, a refetch cadence tied to the
// screen, or a select that narrows the record to the slice it renders.
//
// Every factory starts its key with a prefix no other factory uses, so an
// invalidation by prefix reaches exactly one resource. Every query function
// hands the abort signal to the API client, so a route the reader leaves
// ends the request it started. queries.test.ts holds the first rule and the
// verifier holds the second.

const MINUTE = 60_000;

// Catalog reads change once a day at most. Runtime reads change within a
// minute. The client default in main.tsx covers everything else.
const CATALOG_STALE = 5 * MINUTE;
const RUNTIME_STALE = MINUTE;

// ActivityScope is what the usage page learned about its credential from one
// probe: the admin listing answered, the admin listing refused it, or no
// activity store is configured. A refused own listing surfaces as the error
// of the activity query itself, so the probe needs one request, not two.
export type ActivityScope = "admin" | "own" | "unconfigured";

export type ActivityQuery = {
  scope: ActivityScope;
  filters: ActivityFilters;
  sinceISO?: string;
};

// DOCUMENT_ACTIVITY_LIMIT is the widest activity window the documents panel
// reads in one request. The panel states it so a full page reads as a bound,
// not as the whole history.
export const DOCUMENT_ACTIVITY_LIMIT = 200;

// ACTIVITY_24H_LIMIT bounds the sample the overview folds into its
// sparklines and deltas. A sample that fills the bound is the newest part
// of the day, not the day, and the card hides what the sample cannot show.
export const ACTIVITY_24H_LIMIT = 500;

// widestActivity reads the widest activity listing this credential reaches.
// An admin credential sees the deployment. Every other credential sees the
// account it belongs to.
async function widestActivity(
  filters: ActivityFilters,
  { signal }: api.ReadOptions = {},
): Promise<ActivityPage> {
  try {
    return await api.listAdminActivity(filters, { signal });
  } catch (error) {
    if (!(error instanceof api.ApiError) || !error.needsKey) throw error;
  }
  return api.listActivity(filters, { signal });
}

export const queries = {
  health: () =>
    queryOptions({
      queryKey: ["health"],
      queryFn: ({ signal }) => api.healthReady({ signal }),
      refetchInterval: 30_000,
    }),
  systemInfo: () =>
    queryOptions({ queryKey: ["system-info"], queryFn: ({ signal }) => api.systemInfo({ signal }) }),
  systemMetrics: () =>
    queryOptions({ queryKey: ["system-metrics"], queryFn: ({ signal }) => api.systemMetrics({ signal }) }),
  authMode: () =>
    queryOptions({ queryKey: ["auth-mode"], queryFn: ({ signal }) => api.readAuthMode({ signal }) }),

  models: () =>
    queryOptions({
      queryKey: ["models"],
      queryFn: ({ signal }) => api.listModels({ signal }),
      staleTime: CATALOG_STALE,
    }),
  authors: () =>
    queryOptions({
      queryKey: ["authors"],
      queryFn: ({ signal }) => api.listAuthors({ signal }),
      staleTime: CATALOG_STALE,
    }),
  author: (authorId: string) =>
    queryOptions({
      queryKey: ["author", authorId],
      queryFn: ({ signal }) => api.getAuthor(authorId, { signal }),
      staleTime: CATALOG_STALE,
    }),
  providerCatalog: () =>
    queryOptions({
      queryKey: ["provider-catalog"],
      queryFn: ({ signal }) => api.listProviderCatalog({ signal }),
      staleTime: CATALOG_STALE,
    }),
  catalogMetadata: () =>
    queryOptions({
      queryKey: ["catalog-metadata"],
      queryFn: ({ signal }) => api.catalogMetadata({ signal }),
      staleTime: CATALOG_STALE,
    }),
  catalogChanges: () =>
    queryOptions({
      queryKey: ["catalog-changes"],
      queryFn: ({ signal }) => api.catalogChanges({ signal }),
      staleTime: CATALOG_STALE,
    }),

  providerStatus: () =>
    queryOptions({
      queryKey: ["provider-status"],
      queryFn: ({ signal }) => api.providerStatus({ signal }),
      staleTime: RUNTIME_STALE,
    }),
  providerIncidents: (providerId: string) =>
    queryOptions({
      queryKey: ["provider-incidents", providerId],
      queryFn: ({ signal }) => api.providerIncidentLog(providerId, { signal }),
    }),
  // The window is part of the key so a refetch keeps its bounds.
  providerActivity: (providerId: string, sinceISO: string) =>
    queryOptions({
      queryKey: ["provider-activity", providerId, sinceISO],
      queryFn: ({ signal }) =>
        widestActivity({ provider: providerId, since: sinceISO, limit: 200 }, { signal }),
    }),

  keys: () => queryOptions({ queryKey: ["keys"], queryFn: ({ signal }) => api.listKeys({ signal }) }),
  keyDetail: (keyId: string) =>
    queryOptions({
      queryKey: ["key-detail", keyId],
      queryFn: ({ signal }) => api.getKeyDetail(keyId, { signal }),
    }),
  sharedCredentials: (providerId: string) =>
    queryOptions({
      queryKey: ["shared-credentials", providerId],
      queryFn: ({ signal }) => api.listSharedCredentials(providerId, { signal }),
    }),
  byokCredentials: (accountId: string) =>
    queryOptions({
      queryKey: ["byok", accountId],
      queryFn: ({ signal }) => api.listBYOKCredentials(accountId, { signal }),
    }),

  accounts: () =>
    queryOptions({ queryKey: ["accounts"], queryFn: ({ signal }) => api.listAccounts({ signal }) }),
  accountTemplates: () =>
    queryOptions({
      queryKey: ["account-templates"],
      queryFn: ({ signal }) => api.listAccountTemplates({ signal }),
    }),
  members: () =>
    queryOptions({ queryKey: ["members"], queryFn: ({ signal }) => api.listMembers({ signal }) }),
  memberGrants: (userId: string) =>
    queryOptions({
      queryKey: ["member-grants", userId],
      queryFn: ({ signal }) => api.listMemberGrants(userId, { signal }),
    }),
  reachableAccounts: (userId: string) =>
    queryOptions({
      queryKey: ["reachable-accounts", userId],
      queryFn: ({ signal }) => api.listReachableAccounts(userId, { signal }),
    }),
  teams: () => queryOptions({ queryKey: ["teams"], queryFn: ({ signal }) => api.listTeams({ signal }) }),
  teamMembers: (teamId: string) =>
    queryOptions({
      queryKey: ["team-members", teamId],
      queryFn: ({ signal }) => api.listTeamMembers(teamId, { signal }),
    }),
  teamGrants: (teamId: string) =>
    queryOptions({
      queryKey: ["team-grants", teamId],
      queryFn: ({ signal }) => api.listTeamGrants(teamId, { signal }),
    }),

  files: () => queryOptions({ queryKey: ["files"], queryFn: ({ signal }) => api.listFiles({ signal }) }),
  videoJobs: () =>
    queryOptions({ queryKey: ["video-jobs"], queryFn: ({ signal }) => api.listJobs({ signal }) }),
  documentActivity: () =>
    queryOptions({
      queryKey: ["document-activity"],
      queryFn: async ({ signal }) => (await widestActivity({ limit: DOCUMENT_ACTIVITY_LIMIT }, { signal })).data ?? [],
    }),

  presets: () =>
    queryOptions({
      queryKey: ["presets"],
      queryFn: ({ signal }) => api.listPresets({ signal }),
      staleTime: RUNTIME_STALE,
    }),
  presetHistory: (name: string) =>
    queryOptions({
      queryKey: ["preset-history", name],
      queryFn: ({ signal }) => api.listPresetHistory(name, { signal }),
    }),

  // One probe decides the usage scope for the whole session. A credential
  // change invalidates the client, which is the only event that moves it.
  activityScope: () =>
    queryOptions({
      queryKey: ["activity-scope"],
      queryFn: async ({ signal }): Promise<ActivityScope> => {
        try {
          await api.listAdminActivity({ limit: 1 }, { signal });
          return "admin";
        } catch (error) {
          if (error instanceof api.ApiError && error.status === 503) return "unconfigured";
          if (error instanceof api.ApiError && error.needsKey) return "own";
          throw error;
        }
      },
      staleTime: 30 * MINUTE,
    }),
  activity: ({ scope, filters, sinceISO }: ActivityQuery) =>
    infiniteQueryOptions({
      queryKey: ["activity", { scope, ...filters, since: sinceISO }],
      initialPageParam: "",
      queryFn: ({ pageParam, signal }) => {
        const page = { ...filters, since: sinceISO, cursor: pageParam || undefined };
        return scope === "admin"
          ? api.listAdminActivity(page, { signal })
          : api.listActivity({ ...page, key_id: undefined }, { signal });
      },
      getNextPageParam: (last) => last.next_cursor || undefined,
    }),
  adminActivity24h: () =>
    queryOptions({
      queryKey: ["admin-activity-24h"],
      queryFn: ({ signal }) =>
        api.listAdminActivity(
          { since: new Date(Date.now() - 24 * 3_600_000).toISOString(), limit: ACTIVITY_24H_LIMIT },
          { signal },
        ),
    }),
  // The day before the last one, read the same way, so a stat can say how
  // the day compares.
  adminActivityPrior24h: () =>
    queryOptions({
      queryKey: ["admin-activity-prior-24h"],
      queryFn: ({ signal }) =>
        api.listAdminActivity(
          {
            since: new Date(Date.now() - 48 * 3_600_000).toISOString(),
            until: new Date(Date.now() - 24 * 3_600_000).toISOString(),
            limit: ACTIVITY_24H_LIMIT,
          },
          { signal },
        ),
    }),
  audit: (pageLimit: number) =>
    infiniteQueryOptions({
      queryKey: ["audit"],
      initialPageParam: "",
      queryFn: ({ pageParam, signal }) =>
        api.listAuditLog({ limit: pageLimit, cursor: pageParam || undefined }, { signal }),
      getNextPageParam: (last) => last.next_cursor || undefined,
    }),
};

// retryPolicy is the client default. A gateway answer is final: a 4xx names
// a caller mistake and a 5xx names a provider or store fault that a second
// identical request would repeat within the same second. Only a request that
// never reached the gateway earns one more try.
export function retryPolicy(failureCount: number, error: unknown): boolean {
  if (error instanceof api.ApiError) return false;
  return failureCount < 1;
}

// settle awaits the reads a route loader starts and keeps every outcome with
// the page. The loader exists so a request and the route's code load
// together, not so the router owns failure: a refused read is a state the
// page names, such as a lock behind an admin key, so a rejection resolves
// here and the page reads it from the cache.
export async function settle(...reads: Promise<unknown>[]): Promise<void> {
  await Promise.all(reads.map((read) => read.catch(() => undefined)));
}
