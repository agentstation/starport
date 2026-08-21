import { createFileRoute } from "@tanstack/react-router";

import { PageHeader } from "@/components/shell/PageHeader";

export const Route = createFileRoute("/")({
  component: Placeholder,
});

// CM2 shell placeholder. CM3 replaces this with the overview page.
function Placeholder() {
  return (
    <>
      <PageHeader
        title="Overview"
        description="Gateway status at a glance. The overview page lands in CM3."
      />
      <div className="rounded-md border border-border-1 bg-bg-panel p-6">
        <p className="font-mono text-sm text-text-3">Starport console scaffold</p>
      </div>
    </>
  );
}
