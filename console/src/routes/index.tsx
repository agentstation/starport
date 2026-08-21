import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/")({
  component: Placeholder,
});

// CM1 scaffold placeholder. CM3 replaces this with the overview page.
function Placeholder() {
  return (
    <main className="flex min-h-screen items-center justify-center">
      <p className="font-mono text-sm">Starport console scaffold</p>
    </main>
  );
}
