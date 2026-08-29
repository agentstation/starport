import { createFileRoute, Link } from "@tanstack/react-router";
import { useState } from "react";

import { Card, CardTitle } from "@/components/ui/Card";
import { CopyButton } from "@/components/ui/CopyButton";

export const Route = createFileRoute("/docs")({
  component: DocsPage,
});

// The console's built-in documentation. One page, three readers: a
// developer pointing an SDK at the gateway, an account holder managing
// keys and spend, and an operator running the deployment. A persona
// switcher keeps each reader on one path instead of interleaving all
// three (progressive disclosure over a wall of prose).

type Persona = "build" | "account" | "operate";

const PERSONAS: Array<{ id: Persona; label: string; blurb: string }> = [
  {
    id: "build",
    label: "Build with Starport",
    blurb: "Point an OpenAI or OpenRouter SDK at this gateway and ship.",
  },
  {
    id: "account",
    label: "Manage your account",
    blurb: "API keys, your own provider credentials, and what requests cost.",
  },
  {
    id: "operate",
    label: "Operate the gateway",
    blurb: "Provider credentials, accounts, limits, and deployment health.",
  },
];

function DocsPage() {
  const [persona, setPersona] = useState<Persona>("build");
  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-4">
      <div>
        <h1 className="text-xl font-semibold tracking-[-0.01em]">Documentation</h1>
        <p className="mt-1 text-sm text-text-3">
          How to call, manage, and run the Starport gateway.
        </p>
      </div>
      <div role="tablist" aria-label="Documentation audience" className="flex flex-wrap gap-2">
        {PERSONAS.map((entry) => (
          <button
            key={entry.id}
            role="tab"
            type="button"
            aria-selected={persona === entry.id}
            onClick={() => setPersona(entry.id)}
            className={`flex h-9 items-center rounded-sm border px-3 text-sm transition-colors duration-150 ease-standard ${
              persona === entry.id
                ? "border-border-3 bg-bg-raised text-text-1"
                : "border-border-1 bg-bg-panel text-text-3 hover:border-border-2 hover:text-text-2"
            }`}
          >
            {entry.label}
          </button>
        ))}
      </div>
      <p className="text-sm text-text-3">
        {PERSONAS.find((entry) => entry.id === persona)?.blurb}
      </p>
      {persona === "build" && <BuildDocs />}
      {persona === "account" && <AccountDocs />}
      {persona === "operate" && <OperateDocs />}
    </div>
  );
}

// ---- Shared blocks ----

function CodeBlock({ text }: { text: string }) {
  return (
    <div className="relative rounded-sm border border-border-1 bg-bg-canvas">
      <div className="absolute right-1.5 top-1.5">
        <CopyButton text={() => text} label="snippet" />
      </div>
      <pre className="overflow-x-auto p-3 pr-10 font-mono text-xs leading-4 text-text-2">
        <code>{text}</code>
      </pre>
    </div>
  );
}

function DocSection({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <Card>
      <CardTitle>{title}</CardTitle>
      <div className="flex flex-col gap-3 text-base leading-relaxed text-text-2">
        {children}
      </div>
    </Card>
  );
}

function Term({ children }: { children: React.ReactNode }) {
  return <code className="rounded-xs bg-bg-raised px-1 py-0.5 font-mono text-sm">{children}</code>;
}

// ---- Persona: build ----

function BuildDocs() {
  const origin = location.origin;
  return (
    <>
      <DocSection title="Base URLs">
        <p>
          Starport is a drop-in replacement for OpenRouter and for the OpenAI
          API. Point your existing SDK at this gateway and keep your code:
        </p>
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <code className="font-mono text-sm text-text-1">{`${origin}/v1`}</code>
            <span className="text-sm text-text-3">OpenAI-compatible routes</span>
          </div>
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <code className="font-mono text-sm text-text-1">{`${origin}/api/v1`}</code>
            <span className="text-sm text-text-3">OpenRouter-compatible routes</span>
          </div>
        </div>
        <CodeBlock
          text={`from openai import OpenAI

client = OpenAI(base_url="${origin}/v1", api_key=STARPORT_API_KEY)
reply = client.chat.completions.create(
    model="anthropic/claude-sonnet-5",
    messages=[{"role": "user", "content": "Hello"}],
)`}
        />
      </DocSection>
      <DocSection title="Get a key">
        <p>
          Requests authenticate with a gateway API key. On your own machine,
          the <Term>starport</Term> CLI prints a local admin token:
        </p>
        <CodeBlock text="starport auth token" />
        <p>
          For anything longer-lived, create a scoped key on the{" "}
          <Link to="/keys" className="text-accent-link hover:underline">
            API keys
          </Link>{" "}
          page. A gateway API key identifies the caller — it never pays a
          provider. Provider bills are settled by provider credentials, which
          the operator configures.
        </p>
      </DocSection>
      <DocSection title="Pick a model">
        <p>
          Model IDs take the form <Term>author/model</Term>, for example{" "}
          <Term>anthropic/claude-sonnet-5</Term>. Browse and filter the full
          catalog on the{" "}
          <Link to="/models" className="text-accent-link hover:underline">
            Models
          </Link>{" "}
          page; each row shows context length, price, and what the model can
          do. The gateway routes each request to a provider that serves the
          model and has a usable credential.
        </p>
      </DocSection>
      <DocSection title="Presets">
        <p>
          A preset is a saved routing recipe — model, parameters, and
          fallbacks under one name. Call it by using{" "}
          <Term>@preset/your-preset</Term> as the model. Manage them on the{" "}
          <Link to="/presets" className="text-accent-link hover:underline">
            Presets
          </Link>{" "}
          page.
        </p>
        <CodeBlock
          text={`curl ${origin}/v1/chat/completions \\
  -H "Authorization: Bearer $STARPORT_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "@preset/production", "messages": [{"role": "user", "content": "Hello"}]}'`}
        />
      </DocSection>
      <DocSection title="Beyond chat">
        <p>
          The gateway also serves images, audio, video jobs, document reading,
          reranking, and file storage through the same base URLs. The{" "}
          <Link to="/models" className="text-accent-link hover:underline">
            Models
          </Link>{" "}
          operation facet shows which models serve which operation;{" "}
          <Link to="/files" className="text-accent-link hover:underline">
            Files
          </Link>
          ,{" "}
          <Link to="/jobs" className="text-accent-link hover:underline">
            Jobs
          </Link>
          , and{" "}
          <Link to="/documents" className="text-accent-link hover:underline">
            Documents
          </Link>{" "}
          track that work.
        </p>
      </DocSection>
    </>
  );
}

// ---- Persona: account ----

function AccountDocs() {
  return (
    <>
      <DocSection title="Gateway API keys">
        <p>
          A gateway API key authenticates your requests to the Starport
          gateway — nothing else. Keys carry scopes (what routes they may
          call) and optional spend limits. Create, rotate, and revoke them on
          the{" "}
          <Link to="/keys" className="text-accent-link hover:underline">
            API keys
          </Link>{" "}
          page.
        </p>
      </DocSection>
      <DocSection title="Bring your own provider credential">
        <p>
          By default, your requests are paid by the credentials the operator
          configured. If you bring your own provider credential (BYOK), it
          applies to your account only, and requests it pays are billed to it
          directly. Your account's credential strategy decides the order: the
          default spends the operator's credentials first and falls back to
          yours, while an operator can set your account to prefer your
          credential or to use it exclusively.
        </p>
        <p>
          Add one from a provider's detail page under{" "}
          <Link to="/providers" className="text-accent-link hover:underline">
            Providers
          </Link>
          .
        </p>
      </DocSection>
      <DocSection title="Track usage and spend">
        <p>
          Every request is metered: tokens, cost, latency, cache hits, and
          which credential paid. The{" "}
          <Link to="/usage" className="text-accent-link hover:underline">
            Usage
          </Link>{" "}
          page breaks this down by model, provider, and key over time. A
          cached response costs nothing and shows as a hit.
        </p>
      </DocSection>
      <DocSection title="Stored work">
        <p>
          Uploaded files, asynchronous media jobs, and parsed documents each
          have their own view —{" "}
          <Link to="/files" className="text-accent-link hover:underline">
            Files
          </Link>
          ,{" "}
          <Link to="/jobs" className="text-accent-link hover:underline">
            Jobs
          </Link>
          ,{" "}
          <Link to="/documents" className="text-accent-link hover:underline">
            Documents
          </Link>{" "}
          — with retention windows the operator sets.
        </p>
      </DocSection>
    </>
  );
}

// ---- Persona: operate ----

function OperateDocs() {
  return (
    <>
      <DocSection title="Provider credentials">
        <p>
          A provider credential pays a provider. It is a different thing from
          a gateway API key, which only identifies a caller. Each provider
          resolves its paying credential from three possible sources:
        </p>
        <ol className="flex list-decimal flex-col gap-2 pl-5">
          <li>
            <span className="font-medium text-text-1">Environment</span> — a
            credential read from the process environment (for example{" "}
            <Term>ANTHROPIC_API_KEY</Term>) at startup.
          </li>
          <li>
            <span className="font-medium text-text-1">Gateway</span> — a
            credential you apply in the console for the whole deployment.
          </li>
          <li>
            <span className="font-medium text-text-1">BYOK</span> — a
            credential an account brought for itself. It applies to that
            account only.
          </li>
        </ol>
        <p>
          That is the default order. An account's credential strategy can
          prefer its own BYOK credential, or refuse the operator's credentials
          entirely. Each provider's detail page under{" "}
          <Link to="/providers" className="text-accent-link hover:underline">
            Providers
          </Link>{" "}
          shows every source and which one is paying.
        </p>
      </DocSection>
      <DocSection title="Accounts and limits">
        <p>
          Accounts isolate accounts: each has its own gateway API keys, BYOK
          credentials, usage records, and limits. Create and manage them on
          the{" "}
          <Link to="/accounts" className="text-accent-link hover:underline">
            Accounts
          </Link>{" "}
          page. Spend and rate limits apply per key and per account.
        </p>
      </DocSection>
      <DocSection title="Catalog and routing">
        <p>
          The model catalog comes from a Starmap snapshot — providers, models,
          prices, and capabilities. The gateway refreshes it on a schedule and
          the freshness bar on{" "}
          <Link to="/models" className="text-accent-link hover:underline">
            Models
          </Link>{" "}
          shows the snapshot age. Routing picks a provider per request from
          the catalog plus live availability; there is no hand-maintained
          provider list.
        </p>
      </DocSection>
      <DocSection title="Health and settings">
        <p>
          <Term>GET /health</Term> reports gateway liveness (the sidebar
          status dot reads it). Authentication mode, retention windows, and
          appearance live in{" "}
          <Link to="/settings" className="text-accent-link hover:underline">
            Settings
          </Link>
          . The console itself talks to the gateway through your console
          session — it holds no gateway API key.
        </p>
      </DocSection>
    </>
  );
}
