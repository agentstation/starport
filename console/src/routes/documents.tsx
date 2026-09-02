import { createFileRoute } from "@tanstack/react-router";

import { DocumentsPanel } from "@/components/documents/DocumentsPanel";
import { queries, settle } from "@/lib/queries";

// A document read happens inside a chat request, before the model the caller
// asked for runs. The usage page reports one cost for the whole turn, so this
// is the page that separates what reading the document cost from what
// answering about it cost.

export const Route = createFileRoute("/documents")({
  component: DocumentsPage,
  loader: ({ context }) => settle(context.queryClient.ensureQueryData(queries.documentActivity())),
});

function DocumentsPage() {
  return (
    <div className="flex flex-col gap-4">
      <div>
        <h1 className="text-xl font-semibold tracking-[-0.01em]">Documents</h1>
        <p className="mt-1 text-sm text-text-3">
          A chat request that names the file-parser plugin has its attachments
          read before the model sees them. This page shows which engine read
          them, how many pages it read, and what the pages cost.
        </p>
      </div>
      <DocumentsPanel />
    </div>
  );
}
