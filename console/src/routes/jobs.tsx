import { createFileRoute } from "@tanstack/react-router";

import { JobsPanel } from "@/components/jobs/JobsPanel";
import { queries, settle } from "@/lib/queries";

// A video job is work this gateway holds open across many requests. Every other
// page on this console reads something that finished inside one call, so this
// is the only one where a reader watches a state move.
//
// The page shows the account the credential belongs to. There is no
// deployment-wide job list, because the routes scope every answer to the
// caller.

export const Route = createFileRoute("/jobs")({
  component: JobsPage,
  loader: ({ context }) => settle(context.queryClient.ensureQueryData(queries.videoJobs())),
});

function JobsPage() {
  return (
    <div className="flex flex-col gap-4">
      <div>
        <h1 className="text-xl font-semibold tracking-[-0.01em]">Jobs</h1>
        <p className="mt-1 text-sm text-text-3">
          Video generation runs for minutes, so the gateway answers with a job
          and keeps working. Submit one here, watch its state, and play the
          result while this gateway still holds it.
        </p>
      </div>
      <JobsPanel />
    </div>
  );
}
