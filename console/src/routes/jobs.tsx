import { createFileRoute, useNavigate } from "@tanstack/react-router";

import { BatchesPanel } from "@/components/jobs/BatchesPanel";
import { JobsPanel } from "@/components/jobs/JobsPanel";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { queries, settle } from "@/lib/queries";

// A job is work this gateway holds open across many requests: a video that
// renders for minutes, or a batch that runs a stored file of requests. Every
// other page on this console reads something that finished inside one call,
// so this is the only one where a reader watches a state move.
//
// The page shows the account the credential belongs to. There is no
// deployment-wide job list, because the routes scope every answer to the
// caller.

type JobsTab = "video" | "batches";

type JobsSearch = {
  // tab names the open panel. The video panel is the default and stays out
  // of the address, so the plain link keeps opening on it.
  tab?: "batches";
};

export const Route = createFileRoute("/jobs")({
  component: JobsPage,
  validateSearch: (search: Record<string, unknown>): JobsSearch => ({
    tab: search.tab === "batches" ? "batches" : undefined,
  }),
  loaderDeps: ({ search }) => ({ tab: search.tab }),
  loader: ({ context, deps }) =>
    deps.tab === "batches"
      ? settle(context.queryClient.ensureQueryData(queries.batches()))
      : settle(context.queryClient.ensureQueryData(queries.videoJobs())),
});

function JobsPage() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const tab: JobsTab = search.tab ?? "video";

  return (
    <div className="flex flex-col gap-4">
      <div>
        <h1 className="text-xl font-semibold tracking-[-0.01em]">Jobs</h1>
        <p className="mt-1 text-sm text-text-3">
          Work this gateway keeps running after it answers: a video that
          renders for minutes, or a batch of stored requests. Submit one,
          watch its state, and collect the result while this gateway still
          holds it.
        </p>
      </div>
      <Tabs
        value={tab}
        onValueChange={(value) =>
          void navigate({
            search: { tab: value === "batches" ? "batches" : undefined },
            replace: true,
          })
        }
      >
        <TabsList aria-label="Job kinds">
          <TabsTrigger value="video">Video jobs</TabsTrigger>
          <TabsTrigger value="batches">Batches</TabsTrigger>
        </TabsList>
        <TabsContent value="video">
          <JobsPanel />
        </TabsContent>
        <TabsContent value="batches">
          <BatchesPanel />
        </TabsContent>
      </Tabs>
    </div>
  );
}
