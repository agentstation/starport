import { createFileRoute } from "@tanstack/react-router";

import { FilesPanel } from "@/components/files/FilesPanel";
import { queries, settle } from "@/lib/queries";

// Files are the documents this gateway holds for an account. A chat request
// names one by its identifier instead of carrying its bytes, so the same
// document reaches a model many times without crossing the wire again.
//
// The page shows the account the credential belongs to. There is no
// deployment-wide file list, because the routes scope every answer to the
// caller and a console that read across accounts would need a route that does
// not exist.

export const Route = createFileRoute("/files")({
  component: FilesPage,
  loader: ({ context }) => settle(context.queryClient.ensureQueryData(queries.files())),
});

function FilesPage() {
  return (
    <div className="flex flex-col gap-4">
      <div>
        <h1 className="text-xl font-semibold tracking-[-0.01em]">Files</h1>
        <p className="mt-1 text-sm text-text-3">
          The documents this account stores. A chat request names one by its
          identifier, and the gateway sends the bytes it already holds.
        </p>
      </div>
      <FilesPanel />
    </div>
  );
}
