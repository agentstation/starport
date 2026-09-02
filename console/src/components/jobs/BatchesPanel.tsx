import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, X } from "lucide-react";
import { useState, type ReactNode } from "react";

import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { GhostButton } from "@/components/ui/Form";
import { Pill, type PillTone } from "@/components/ui/Pill";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { TableSkeleton } from "@/components/ui/skeleton";
import {
  accessMessage,
  ApiError,
  cancelBatch,
  fetchStoredFile,
  TERMINAL_BATCH_STATES,
  type Batch,
} from "@/lib/api";
import { announce, report } from "@/lib/mutations";
import { queries } from "@/lib/queries";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

// A batch is the second kind of work this gateway holds open across many
// requests. A caller uploads a JSONL file of requests, submits it, and
// collects an output file and an error file once every line has run. This
// panel shows what is still running, how far each batch got, and what a
// reader can still collect.

// POLL_MS is how often an unfinished batch is re-read. A batch runs for
// minutes, so a faster poll would spend requests to learn nothing. A panel
// holding only terminal batches stops polling: those answers never change.
const POLL_MS = 5_000;

function terminal(batch: Batch): boolean {
  return TERMINAL_BATCH_STATES.includes(batch.status);
}

// STATUS_TONE maps the batch states to a lifecycle tone. OpenAI names a
// queued batch "validating" and a working one "in_progress"; both read as
// motion rather than a fault. An expired batch ran out of its window, which
// is a warning rather than a failure of the work itself.
const STATUS_TONE: Record<string, PillTone> = {
  validating: "neutral",
  in_progress: "info",
  finalizing: "info",
  completed: "success",
  failed: "error",
  expired: "warning",
  cancelling: "neutral",
  cancelled: "neutral",
};

function refusalText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.needsKey || error.forbidden) return accessMessage(error, "batches:write");
    return error.message;
  }
  return error instanceof Error ? error.message : String(error);
}

// reasonText reads why a failed batch stopped before its lines did. The
// wire carries a list, and this gateway writes one entry.
function reasonText(batch: Batch): string | undefined {
  return batch.errors?.data?.find((entry) => entry.message)?.message;
}

// BatchProgress is the completed, failed, and total bar. Completed lines
// fill from the left in success green and failed lines follow in error red,
// so the unfilled remainder is what has not run yet. The words beside it
// carry the same three counts, because a bar alone is not a number.
export function BatchProgress({ counts }: { counts: Batch["request_counts"] }) {
  const total = Math.max(0, counts.total);
  const completed = Math.min(total, Math.max(0, counts.completed));
  const failed = Math.min(total - completed, Math.max(0, counts.failed));
  const done = completed + failed;
  const percent = (value: number) => (total > 0 ? (value / total) * 100 : 0);
  return (
    <div className="flex items-center gap-2" data-testid="batch-progress">
      <span
        role="meter"
        aria-label="Lines run"
        aria-valuemin={0}
        aria-valuemax={total}
        aria-valuenow={done}
        aria-valuetext={`${completed} completed, ${failed} failed, ${total} total`}
        className="flex h-1.5 w-24 shrink-0 overflow-hidden rounded-full bg-border-3"
      >
        <span className="block h-full bg-success" style={{ width: `${percent(completed)}%` }} />
        <span className="block h-full bg-error" style={{ width: `${percent(failed)}%` }} />
      </span>
      <span className="text-xs tabular-nums text-text-3">
        {completed} of {total} completed
        {failed > 0 && <span className="text-error"> · {failed} failed</span>}
      </span>
    </div>
  );
}

// FileDownload collects one stored file of a batch. The bytes come through a
// fetch under the held credential and land as a browser download, because a
// plain link would send no Authorization header.
function FileDownload({
  fileID,
  label,
  filename,
}: {
  fileID: string;
  label: string;
  filename: string;
}) {
  const [busy, setBusy] = useState(false);
  const collect = async () => {
    setBusy(true);
    try {
      const blob = await fetchStoredFile(fileID);
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = filename;
      document.body.append(anchor);
      anchor.click();
      anchor.remove();
      URL.revokeObjectURL(url);
    } catch (error) {
      report(`${label} download failed: ${refusalText(error)}`);
    } finally {
      setBusy(false);
    }
  };
  return (
    <GhostButton onClick={() => void collect()} disabled={busy} title={`Download ${fileID}`}>
      <Download className="mr-1.5 size-3.5" />
      {busy ? "Fetching…" : label}
    </GhostButton>
  );
}

export function BatchesPanel() {
  const access = useGatewayAccess();
  const queryClient = useQueryClient();

  const batches = useQuery({
    ...queries.batches(),
    enabled: access,
    refetchInterval: (query) => {
      const listed = query.state.data?.batches ?? [];
      return listed.some((batch) => !terminal(batch)) ? POLL_MS : false;
    },
  });

  // A cancel opens a dialog first. The write travels only from there, and a
  // refusal stays in the dialog's error slot until the operator closes it.
  const [cancelling, setCancelling] = useState<Batch | null>(null);
  const stop = useMutation({
    mutationFn: (batch: Batch) => cancelBatch(batch.id),
    onSuccess: async (_result, batch) => {
      setCancelling(null);
      announce(`Cancelled ${batch.id}`);
      await queryClient.invalidateQueries({ queryKey: queries.batches().queryKey });
    },
  });

  const rows = batches.data?.batches ?? [];

  let body: ReactNode;
  if (batches.error) {
    body = (
      <p className="text-base text-text-3">
        {batches.error instanceof ApiError && batches.error.needsKey
          ? accessMessage(batches.error, "batches:write")
          : `Failed to load batches: ${batches.error.message}`}
      </p>
    );
  } else if (batches.isPending) {
    body = <TableSkeleton columns={6} />;
  } else if (rows.length === 0) {
    body = (
      <p className="text-base text-text-3">
        This account has run no batches. Upload a JSONL file with purpose{" "}
        <code className="font-mono text-xs">batch</code> and submit it to{" "}
        <code className="font-mono text-xs">POST /v1/batches</code>. The batch
        keeps running after this page closes, and its files wait here.
      </p>
    );
  } else {
    body = (
      <div className="overflow-x-auto rounded-md border border-border-1 bg-bg-panel">
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-border-1 text-left text-xs font-medium text-text-3">
              <th scope="col" className="px-4 py-2.5">Batch</th>
              <th scope="col" className="px-4 py-2.5">Endpoint</th>
              <th scope="col" className="px-4 py-2.5">State</th>
              <th scope="col" className="px-4 py-2.5">Progress</th>
              <th scope="col" className="px-4 py-2.5">Created</th>
              <th scope="col" className="px-4 py-2.5" />
            </tr>
          </thead>
          <tbody>
            {rows.map((batch) => {
              const reason = reasonText(batch);
              return (
                <tr
                  key={batch.id}
                  data-testid="batch-row"
                  className="border-b border-border-1 last:border-0 align-top"
                >
                  <td className="px-4 py-2.5 font-mono text-xs text-text-2">{batch.id}</td>
                  <td className="px-4 py-2.5 font-mono text-xs text-text-2">{batch.endpoint}</td>
                  <td className="px-4 py-2.5">
                    <Pill tone={STATUS_TONE[batch.status] ?? "neutral"}>
                      {batch.status.replaceAll("_", " ")}
                    </Pill>
                  </td>
                  <td className="px-4 py-2.5">
                    <div className="flex flex-col gap-1.5">
                      <BatchProgress counts={batch.request_counts} />
                      {reason && (
                        <p data-testid="batch-failure" className="text-xs text-error">
                          {reason}
                        </p>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-2.5 text-xs text-text-3">
                    <RelativeTime iso={new Date(batch.created_at * 1000).toISOString()} />
                  </td>
                  <td className="px-4 py-2.5">
                    <div className="flex items-center justify-end gap-2">
                      {batch.output_file_id && (
                        <FileDownload
                          fileID={batch.output_file_id}
                          label="Output file"
                          filename={`${batch.id}-output.jsonl`}
                        />
                      )}
                      {batch.error_file_id && (
                        <FileDownload
                          fileID={batch.error_file_id}
                          label="Error file"
                          filename={`${batch.id}-errors.jsonl`}
                        />
                      )}
                      {!terminal(batch) && (
                        <GhostButton onClick={() => setCancelling(batch)}>
                          <X className="size-3.5" />
                          Cancel
                        </GhostButton>
                      )}
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {body}
      {batches.data?.capped && (
        <p className="text-xs text-text-4">
          The listing is capped, so this shows the newest batches only.
        </p>
      )}
      {cancelling && (
        <ConfirmDialog
          title="Cancel batch"
          action="Cancel batch"
          dismiss="Keep running"
          error={stop.error ? `Cancel failed: ${refusalText(stop.error)}` : ""}
          pending={stop.isPending}
          onConfirm={() => stop.mutate(cancelling)}
          onClose={() => {
            stop.reset();
            setCancelling(null);
          }}
        >
          <p>
            Cancel <strong className="text-text-1">{cancelling.id}</strong>? Lines
            already running finish and keep their results. No new line starts,
            and the batch stays in the list as cancelled.
          </p>
        </ConfirmDialog>
      )}
    </div>
  );
}
