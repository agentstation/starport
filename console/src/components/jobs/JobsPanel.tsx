import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Play, Send, X } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";

import {
  INPUT_CLASS,
  GhostButton,
  PrimaryButton,
} from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import {
  accessMessage,
  ApiError,
  cancelJob,
  fetchJobAsset,
  listJobs,
  listModels,
  submitJob,
  TERMINAL_JOB_STATES,
  VIDEO_OPERATION,
  type Model,
  type VideoJob,
} from "@/lib/api";
import { formatMs, formatUnixTime } from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

// A video job is the one piece of work in this gateway that outlives the
// request that started it. Everything else on this console answers inside one
// call, so this page is the only one that has to show a reader what is still
// running, what ended badly, and what it can still play.

const JOBS_KEY = ["video-jobs"];

// POLL_MS is how often an unfinished job is re-read. A video takes minutes, so
// a faster poll would spend requests to learn nothing. A page holding only
// terminal jobs stops polling altogether: those answers never change again.
const POLL_MS = 5_000;

// videoModels keeps the submit control honest. Offering every model would let a
// reader submit a chat model and read a routing refusal that says nothing about
// the mistake. The catalog names what each offering serves, so the list is what
// the deployment can actually run.
function videoModels(models: Model[]): Model[] {
  return models.filter((model) =>
    (model.offerings ?? []).some((offering) =>
      (offering.operations ?? []).includes(VIDEO_OPERATION),
    ),
  );
}

function terminal(job: VideoJob): boolean {
  return TERMINAL_JOB_STATES.includes(job.status);
}

// playable reports whether this gateway still holds bytes for the job.
//
// The window is the whole answer. It is absent while nothing is stored and
// absent again once the bytes go, and a window in the past means the sweep has
// not run yet rather than that the video is readable.
function playable(job: VideoJob, now: number): boolean {
  if (job.status !== "completed") return false;
  return job.expires_at !== undefined && job.expires_at * 1000 > now;
}

// elapsed is how long the job has been alive. A running job counts against the
// clock and a finished one counts against the moment it ended, which is what
// makes the number stop moving when the work does.
function elapsed(job: VideoJob, now: number): number {
  const ended = job.completed_at ? job.completed_at * 1000 : now;
  return Math.max(0, ended - job.created_at * 1000);
}

// refusalText reads the reason the gateway gave. A refused submission is the
// place a generic message costs the reader the answer: an account at its
// outstanding job limit, a model that serves no video, and a missing scope each
// need a different next step.
function refusalText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.needsKey || error.forbidden) return accessMessage(error, "videos:write");
    return error.message;
  }
  return error instanceof Error ? error.message : String(error);
}

const STATUS_CLASS: Record<string, string> = {
  completed: "text-success",
  failed: "text-error",
  cancelled: "text-text-3",
};

// JobPlayer holds the bytes of one video while a reader watches it.
//
// The bytes come through a fetch rather than a `src` on the element, because a
// player sends no Authorization header and a reader holding a pasted gateway
// key would get a refusal. The object URL is revoked on unmount: a page left
// open through a dozen videos would otherwise hold all of them.
export function JobPlayer({ jobID }: { jobID: string }) {
  const [source, setSource] = useState<string | null>(null);
  const [failed, setFailed] = useState<string | null>(null);

  useEffect(() => {
    let url: string | null = null;
    let dropped = false;
    fetchJobAsset(jobID)
      .then((blob) => {
        if (dropped) return;
        url = URL.createObjectURL(blob);
        setSource(url);
      })
      .catch((error: unknown) => {
        if (!dropped) setFailed(refusalText(error));
      });
    return () => {
      dropped = true;
      if (url) URL.revokeObjectURL(url);
    };
  }, [jobID]);

  if (failed) {
    return (
      <p data-testid="job-play-failure" className="text-sm text-error">
        {failed}
      </p>
    );
  }
  if (!source) {
    return <p className="text-sm text-text-3">Loading the video…</p>;
  }
  return (
    <video
      data-testid="job-player"
      src={source}
      controls
      className="max-h-96 w-full rounded-md border border-border-1 bg-black"
    />
  );
}

export function JobsPanel() {
  const access = useGatewayAccess();
  const queryClient = useQueryClient();
  const [model, setModel] = useState("");
  const [prompt, setPrompt] = useState("");
  const [playing, setPlaying] = useState<string | null>(null);
  const [notice, setNotice] = useState<{ text: string; error?: boolean } | null>(
    null,
  );

  // The clock is state rather than a read at render time, because a running
  // job's elapsed time has to move without anything else changing. A poll would
  // move it too, but only every few seconds, and a stopwatch that jumps in
  // five-second steps reads as a frozen page.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const tick = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(tick);
  }, []);

  const jobs = useQuery({
    queryKey: JOBS_KEY,
    queryFn: listJobs,
    enabled: access,
    retry: false,
    refetchInterval: (query) => {
      const listed = query.state.data?.jobs ?? [];
      return listed.some((job) => !terminal(job)) ? POLL_MS : false;
    },
  });

  const models = useQuery({
    queryKey: ["models"],
    queryFn: listModels,
    enabled: access,
    retry: false,
  });
  const servers = videoModels(models.data ?? []);

  const submit = useMutation({
    mutationFn: () => submitJob(model, prompt),
    onSuccess: async (job) => {
      setNotice({ text: `Submitted ${job.id}` });
      setPrompt("");
      await queryClient.invalidateQueries({ queryKey: JOBS_KEY });
    },
    onError: (error) =>
      setNotice({ text: `Submission refused: ${refusalText(error)}`, error: true }),
  });

  const stop = useMutation({
    mutationFn: (job: VideoJob) => cancelJob(job.id),
    onSuccess: async (_result, job) => {
      setNotice({ text: `Cancelled ${job.id}` });
      await queryClient.invalidateQueries({ queryKey: JOBS_KEY });
    },
    onError: (error) =>
      setNotice({ text: `Cancel failed: ${refusalText(error)}`, error: true }),
  });

  const rows = jobs.data?.jobs ?? [];

  let body: ReactNode;
  if (jobs.error) {
    body = (
      <p className="text-base text-text-3">
        {jobs.error instanceof ApiError && jobs.error.needsKey
          ? accessMessage(jobs.error, "videos:write")
          : `Failed to load jobs: ${jobs.error.message}`}
      </p>
    );
  } else if (jobs.isPending) {
    body = <p className="text-base text-text-3">Loading jobs…</p>;
  } else if (rows.length === 0) {
    body = (
      <p className="text-base text-text-3">
        This account has run no video jobs. Submit one above; it keeps running
        after this page closes, and the result waits here.
      </p>
    );
  } else {
    body = (
      <div className="overflow-x-auto rounded-md border border-border-1 bg-bg-panel">
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-border-1 text-left text-xs font-medium text-text-3">
              <th className="px-4 py-2.5">Job</th>
              <th className="px-4 py-2.5">Model</th>
              <th className="px-4 py-2.5">State</th>
              <th className="px-4 py-2.5">Elapsed</th>
              <th className="px-4 py-2.5">Result</th>
              <th className="px-4 py-2.5" />
            </tr>
          </thead>
          <tbody>
            {rows.map((job) => (
              <tr
                key={job.id}
                data-testid="job-row"
                className="border-b border-border-1 last:border-0 align-top"
              >
                <td className="px-4 py-2 font-mono text-xs text-text-2">
                  {job.id}
                </td>
                <td className="px-4 py-2 text-text-2">{job.model}</td>
                <td
                  className={`px-4 py-2 text-xs ${STATUS_CLASS[job.status] ?? "text-text-2"}`}
                >
                  {job.status}
                </td>
                <td className="px-4 py-2 tabular-nums text-xs text-text-3">
                  {formatMs(elapsed(job, now))}
                </td>
                <td className="px-4 py-2">
                  {job.status === "failed" && (
                    <p data-testid="job-failure" className="text-xs text-error">
                      {job.error?.message ??
                        "The provider gave no reason for the failure."}
                    </p>
                  )}
                  {job.status === "completed" && !playable(job, now) && (
                    <p data-testid="job-expired" className="text-xs text-text-3">
                      This gateway no longer holds the video. The work finished
                      and the retention window closed.
                    </p>
                  )}
                  {playable(job, now) && (
                    <div className="flex flex-col gap-2">
                      <p className="text-xs text-text-3">
                        Playable until {formatUnixTime(job.expires_at)}
                      </p>
                      {playing === job.id && <JobPlayer jobID={job.id} />}
                    </div>
                  )}
                </td>
                <td className="px-4 py-2">
                  <div className="flex items-center justify-end gap-2">
                    {playable(job, now) && playing !== job.id && (
                      <GhostButton onClick={() => setPlaying(job.id)}>
                        <Play className="size-3.5" />
                        Play
                      </GhostButton>
                    )}
                    {!terminal(job) && (
                      <GhostButton
                        onClick={() => stop.mutate(job)}
                        disabled={stop.isPending}
                      >
                        <X className="size-3.5" />
                        Cancel
                      </GhostButton>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-2">
        <label className="sr-only" htmlFor="job-model">
          Model
        </label>
        <Select
          id="job-model"
          value={model}
          onChange={(event) => setModel(event.target.value)}
        >
          <option value="">Choose a model</option>
          {servers.map((option) => (
            <option key={option.id} value={option.id}>
              {option.id}
            </option>
          ))}
        </Select>
        <label className="sr-only" htmlFor="job-prompt">
          Prompt
        </label>
        <input
          id="job-prompt"
          value={prompt}
          placeholder="Describe the video"
          onChange={(event) => setPrompt(event.target.value)}
          className={`${INPUT_CLASS} min-w-64 flex-1`}
        />
        <PrimaryButton
          onClick={() => submit.mutate()}
          disabled={submit.isPending || model === "" || prompt.trim() === ""}
        >
          <Send className="size-4" />
          {submit.isPending ? "Submitting…" : "Submit job"}
        </PrimaryButton>
      </div>
      {models.isSuccess && servers.length === 0 && (
        <p className="text-sm text-text-3">
          No model this deployment routes to serves video generation. The
          catalog names what each offering serves, so a provider that gains one
          shows up here without a console change.
        </p>
      )}
      {notice && (
        <p
          data-testid="job-notice"
          className={`text-sm ${notice.error ? "text-error" : "text-success"}`}
        >
          {notice.text}
        </p>
      )}
      {body}
      {jobs.data?.capped && (
        <p className="text-xs text-text-4">
          The listing is capped, so this shows the newest jobs only.
        </p>
      )}
    </div>
  );
}
