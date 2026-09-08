import { createFileRoute, Link } from "@tanstack/react-router";
import { type ComponentProps, type ReactNode, useSyncExternalStore } from "react";

import { CopyButton } from "@/components/ui/CopyButton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { appliedTheme, onThemeChange, setTheme } from "@/lib/theme";

export const Route = createFileRoute("/docs")({
  validateSearch: (search: Record<string, unknown>): { audience?: Persona } => ({
    audience: search.audience === "account" || search.audience === "operate"
      ? search.audience
      : "build",
  }),
  component: DocsPage,
});

// Static documentation has separate paths for developers, account holders,
// and operators. The route tests require links to every console destination.

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
    blurb: "Provider credentials, accounts, teams, policy, and deployment health.",
  },
];

function DocsPage() {
  const { audience: persona = "build" } = Route.useSearch();
  const navigate = Route.useNavigate();
  const theme = useSyncExternalStore(onThemeChange, appliedTheme);
  return (
    <main className="documentation mx-auto flex w-full flex-col gap-6">
      <nav aria-label="Documentation navigation" className="flex flex-wrap items-center justify-between gap-3">
        <span className="font-semibold text-text-1">Starport</span>
        <div className="flex flex-wrap items-center gap-4">
          <button type="button" className="text-text-2 hover:underline"
            aria-label={`Switch to ${theme === "dark" ? "light" : "dark"} theme`}
            onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
            {theme === "dark" ? "Dark theme" : "Light theme"}
          </button>
          <Link to="/" preload={false} className="text-accent-link hover:underline">Open console</Link>
        </div>
      </nav>
      <div>
        <h1 className="font-semibold tracking-[-0.01em]">Documentation</h1>
        <p className="mt-2 text-text-2">
          How to call, manage, and run the Starport gateway.
        </p>
      </div>
      <Tabs value={persona} onValueChange={(value) => void navigate({ search: { audience: value as Persona }, hash: "" })}>
        <TabsList variant="chips" aria-label="Documentation audience">
          {PERSONAS.map((entry) => (
            <TabsTrigger key={entry.id} value={entry.id}>
              {entry.label}
            </TabsTrigger>
          ))}
        </TabsList>
        <p className="text-text-2">
          {PERSONAS.find((entry) => entry.id === persona)?.blurb}
        </p>
        <TabsContent value="build" className="flex flex-col gap-6 pt-2">
          <BuildDocs />
        </TabsContent>
        <TabsContent value="account" className="flex flex-col gap-6 pt-2">
          <AccountDocs />
        </TabsContent>
        <TabsContent value="operate" className="flex flex-col gap-6 pt-2">
          <OperateDocs />
        </TabsContent>
      </Tabs>
    </main>
  );
}

// ---- Shared blocks ----

function CodeBlock({ text, language = "Shell" }: { text: string; language?: string }) {
  return (
    <div className="min-w-0 rounded-sm border border-border-1 bg-bg-canvas">
      <div className="flex items-center justify-between border-b border-border-1 px-3 text-text-2">
        <span>{language}</span>
        <CopyButton text={() => text} label="snippet" />
      </div>
      <pre tabIndex={0} aria-label={`${language} example`} className="overflow-x-auto p-3 font-mono text-text-2">
        <code>{text}</code>
      </pre>
    </div>
  );
}

// Sections provide stable heading links and a continuous reading column.
function DocSection({ title, children }: { title: string; children: ReactNode }) {
  const id = `doc-${title.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/-$/, "")}`;
  return (
    <section className="flex flex-col gap-3 border-t border-border-1 pt-5 first:border-t-0 first:pt-0">
      <h2 id={id} tabIndex={-1} className="font-semibold tracking-[-0.01em] text-text-1"><a href={`#${id}`}>{title}</a></h2>
      <div className="docs-prose flex flex-col gap-3 text-text-2">
        {children}
      </div>
    </section>
  );
}

function Term({ children }: { children: ReactNode }) {
  return <code className="rounded-xs bg-bg-raised px-1 py-0.5 font-mono text-sm">{children}</code>;
}

function PageLink({ to, children }: { to: ComponentProps<typeof Link>["to"]; children: ReactNode }) {
  return (
    <Link to={to} preload={false} className="text-accent-link hover:underline">
      {children}
    </Link>
  );
}

// The table maps gateway API key scopes to their routes.
// The keys page grants this inference set to a non-admin key.
const SCOPES: Array<{ scope: string; opens: string }> = [
  { scope: "chat:write", opens: "Chat completions and responses" },
  { scope: "embeddings:write", opens: "Embeddings" },
  { scope: "images:write", opens: "Image generation and edits" },
  { scope: "audio:write", opens: "Speech, transcription, and translation" },
  { scope: "rerank:write", opens: "Reranking" },
  { scope: "moderations:write", opens: "Moderation of the caller's own text" },
  { scope: "batches:write", opens: "Batch jobs: create, list, and cancel" },
  { scope: "models:read", opens: "The model catalog" },
  { scope: "activity:read", opens: "The key's own usage records" },
  { scope: "files:read", opens: "Read stored files" },
  { scope: "files:write", opens: "Upload and delete stored files" },
  { scope: "admin", opens: "Every admin route, including keys, accounts, teams, and the audit log" },
];

function ScopeTable() {
  return (
    <div className="overflow-x-auto rounded-sm border border-border-1">
      <table className="w-full text-sm">
        <thead className="bg-bg-raised text-left text-xs font-medium text-text-2">
          <tr>
            <th scope="col" className="px-3 py-2">
              Scope
            </th>
            <th scope="col" className="px-3 py-2">
              Opens
            </th>
          </tr>
        </thead>
        <tbody>
          {SCOPES.map((row) => (
            <tr key={row.scope} className="border-t border-border-1">
              <td className="whitespace-nowrap px-3 py-1.5 font-mono text-xs text-text-1">
                {row.scope}
              </td>
              <td className="px-3 py-1.5 text-text-2">{row.opens}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
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
        <p>
          Keep the key in the <Term>STARPORT_API_KEY</Term> environment
          variable and read it from there, so no source file carries it.
        </p>
        <CodeBlock
          language="Python"
          text={`import os
from openai import OpenAI

client = OpenAI(
    base_url="${origin}/v1",
    api_key=os.environ["STARPORT_API_KEY"],
)
reply = client.chat.completions.create(
    model="anthropic/claude-sonnet-5",
    messages=[{"role": "user", "content": "Hello"}],
)`}
        />
        <CodeBlock
          language="JavaScript"
          text={`import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "${origin}/v1",
  apiKey: process.env.STARPORT_API_KEY,
});
const reply = await client.chat.completions.create({
  model: "anthropic/claude-sonnet-5",
  messages: [{ role: "user", content: "Hello" }],
});`}
        />
      </DocSection>
      <DocSection title="Get a key">
        <p>
          Requests authenticate with a gateway API key. On the gateway machine,
          open the console through its local launch link:
        </p>
        <CodeBlock text="starport ui" />
        <p>
          Create a scoped gateway key on the <PageLink to="/keys">API keys</PageLink>{" "}
          page and store it in <Term>STARPORT_API_KEY</Term> for your SDK.
          The local admin token grants console access. It cannot authenticate inference requests.
        </p>
        <p>
          A gateway API key identifies the caller. A separate provider credential pays for inference.
          If you do not operate the gateway, ask its administrator for a scoped gateway key.
        </p>
      </DocSection>
      <DocSection title="Pick a model">
        <p>
          Model IDs take the form <Term>author/model</Term>, for example{" "}
          <Term>anthropic/claude-sonnet-5</Term>. Browse and filter the full
          catalog on the <PageLink to="/models">Models</PageLink> page; each
          row shows context length, price, and what the model can do. The{" "}
          <PageLink to="/authors">Authors</PageLink> page groups the same
          catalog by the organization that trained each model.
        </p>
        <p>
          The gateway routes each request to a provider that serves the model
          and has a usable credential. Try a model in{" "}
          <PageLink to="/chat">Chat</PageLink> before you write code: the
          conversation runs through the same route your SDK will take.
        </p>
      </DocSection>
      <DocSection title="Presets">
        <p>
          A preset is a saved routing recipe: model, parameters, and
          fallbacks under one name. Call it by using{" "}
          <Term>@preset/your-preset</Term> as the model. Manage them on the{" "}
          <PageLink to="/presets">Presets</PageLink> page.
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
          The gateway also serves embeddings, images, audio, video jobs,
          document reading, reranking, moderation, batches, and file storage
          through the same base URLs. The{" "}
          <PageLink to="/models">Models</PageLink> operation facet shows which
          models serve which operation. <PageLink to="/files">Files</PageLink>,{" "}
          <PageLink to="/jobs">Jobs</PageLink>, and{" "}
          <PageLink to="/documents">Documents</PageLink> track that work.
        </p>
        <p>
          A batch takes a file of requests and answers a file of results, at
          the batch price where a provider offers one. The Jobs page lists
          batches beside video jobs.
        </p>
        <CodeBlock
          text={`curl ${origin}/v1/batches \\
  -H "Authorization: Bearer $STARPORT_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"input_file_id": "file-abc123", "endpoint": "/v1/chat/completions", "completion_window": "24h"}'`}
        />
      </DocSection>
      <DocSection title="Use it from a coding agent">
        <p>
          The binary carries an agent skill that teaches a coding agent to
          install, start, query, and diagnose this gateway. Install it once
          into the shared skills root, and again after a CLI upgrade so the
          skill tracks the installed commands:
        </p>
        <CodeBlock text={`starport agent setup`} />
        <p>
          The CLI also answers catalog questions offline from the embedded
          catalog, so an agent can find a model without a network call or a
          key:
        </p>
        <CodeBlock
          text={`starport models search gpt-4o --json
starport models show openai/gpt-4o-mini --json`}
        />
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
          gateway and nothing else. Keys carry scopes (what routes they may
          call) and optional spend limits. Create, rotate, and revoke them on
          the <PageLink to="/keys">API keys</PageLink> page. A new key gets the
          whole inference set below; an admin key gets{" "}
          <Term>admin</Term> alone.
        </p>
        <ScopeTable />
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
          <PageLink to="/providers">Providers</PageLink>.
        </p>
      </DocSection>
      <DocSection title="Track usage and spend">
        <p>
          Every request is metered: tokens, cost, latency, cache hits, and
          which credential paid. The <PageLink to="/usage">Usage</PageLink>{" "}
          page groups this data by model, provider, and key over time.
          An exact response-cache hit avoids another completion call.
          A semantic-cache lookup can require a paid embedding.
        </p>
        <p>
          A key, an account, or a team can carry a spend budget for a fixed
          window. A request past the budget answers 402 and names the budget
          in the message, and the Usage page shows how much of each window
          is spent.
        </p>
      </DocSection>
      <DocSection title="Stored work">
        <p>
          Uploaded files, asynchronous media jobs, and parsed documents each
          have their own view: <PageLink to="/files">Files</PageLink>,{" "}
          <PageLink to="/jobs">Jobs</PageLink>, and{" "}
          <PageLink to="/documents">Documents</PageLink>, with retention
          windows the operator sets.
        </p>
      </DocSection>
    </>
  );
}

// ---- Persona: operate ----

function OperateDocs() {
  const origin = location.origin;
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
            <span className="font-medium text-text-1">Environment</span>: a
            credential read from the process environment (for example{" "}
            <Term>ANTHROPIC_API_KEY</Term>) at startup.
          </li>
          <li>
            <span className="font-medium text-text-1">Shared</span>: a
            credential you apply in the console for the whole deployment.
          </li>
          <li>
            <span className="font-medium text-text-1">BYOK</span>: a
            credential an account brought for itself. It applies to that
            account only.
          </li>
        </ol>
        <p>
          That is the default order. An account's credential strategy can
          prefer its own BYOK credential, or refuse the operator's credentials
          entirely. Each provider's detail page under{" "}
          <PageLink to="/providers">Providers</PageLink> shows every source
          and which one is paying.
        </p>
      </DocSection>
      <DocSection title="Accounts and limits">
        <p>
          Accounts isolate tenants: each has its own gateway API keys, BYOK
          credentials, usage records, and limits. Create and manage them on
          the <PageLink to="/accounts">Accounts</PageLink> page. Spend and
          rate limits apply per key and per account.
        </p>
      </DocSection>
      <DocSection title="Policy">
        <p>
          Each account carries a policy with two rules: whether it may bring
          its own provider credentials, and which providers and models its
          requests may reach. Both default to everything. Narrow them from
          the account's page under <PageLink to="/accounts">Accounts</PageLink>
          ; a request outside the access list is refused before any provider
          sees it.
        </p>
        <p>
          Guardrails apply to the whole deployment. A configured check reads
          each request and each answer and allows, redacts, or refuses; a
          refusal answers 400 with the check name. Guardrails stay off until
          the environment names a check:
        </p>
        <CodeBlock text={`export STARPORT_GUARDRAILS_CHECKS="pii,moderation"`} />
      </DocSection>
      <DocSection title="Teams and budgets">
        <p>
          <PageLink to="/members">Members</PageLink> are the people your
          identity provider resolved; the gateway never invents one. A team
          on the <PageLink to="/teams">Teams</PageLink> page groups members,
          and granting an account to a team grants it to everyone on the
          roster, now and later.
        </p>
        <p>
          A team budget bounds spend over a day, a week, or a month, summed
          over every key attributed to the team across every account the
          team reaches. Limits are in nano-USD, so five dollars is{" "}
          <Term>5000000000</Term>:
        </p>
        <CodeBlock
          text={`curl -X PUT ${origin}/api/v1/admin/teams/TEAM_ID \\
  -H "Authorization: Bearer $STARPORT_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"name": "platform", "budget": {"limit": 5000000000, "interval": "month"}}'`}
        />
      </DocSection>
      <DocSection title="Catalog and routing">
        <p>
          The model catalog comes from a Starmap snapshot: providers, models,
          prices, and capabilities. The gateway refreshes it on a schedule and
          the freshness bar on <PageLink to="/models">Models</PageLink> shows
          the snapshot age. Routing picks a provider per request from the
          catalog plus live availability; there is no hand-maintained
          provider list.
        </p>
      </DocSection>
      <DocSection title="Audit log">
        <p>
          Every admin mutation leaves one durable record: who asked, what it
          touched, and whether the store accepted it. The trail covers keys,
          accounts, templates, teams, memberships, grants, credentials,
          presets, and the authentication mode, and a record never holds a
          credential value. Read it on the{" "}
          <PageLink to="/audit">Audit log</PageLink> page, or from the admin
          route with the same filters:
        </p>
        <CodeBlock
          text={`curl "${origin}/api/v1/admin/audit?action=key.create&limit=50" \\
  -H "Authorization: Bearer $STARPORT_API_KEY"`}
        />
        <p>
          Each record carries the <Term>request_id</Term> of the request that
          made the change, and the <PageLink to="/usage">Usage</PageLink> page
          takes the same ID to reach the matching request.
        </p>
      </DocSection>
      <DocSection title="Observability">
        <p>
          The gateway serves a Prometheus scrape at <Term>GET /metrics</Term>{" "}
          with no credentials, the same way the health checks do. Labels
          name the protocol, operation, provider, and model, never an
          account or a key. Set <Term>STARPORT_TELEMETRY_METRICS</Term> to{" "}
          <Term>admin</Term> to require the admin scope, or to{" "}
          <Term>off</Term> to remove the route.
        </p>
        <CodeBlock text={`curl ${origin}/metrics`} />
        <p>
          Traces leave over OTLP HTTP once the standard OpenTelemetry endpoint
          variable is set. One chat request produces a request span, a route
          plan span, one span per attempt, and one per provider call, and an
          inbound <Term>traceparent</Term> header joins the caller's trace.
        </p>
        <CodeBlock text={`export OTEL_EXPORTER_OTLP_ENDPOINT="http://127.0.0.1:4318"`} />
        <p>
          Webhooks push budget, job, provider health, and key events to a URL
          you register under the admin scope, so an outside system learns of
          a change without polling.
        </p>
      </DocSection>
      <DocSection title="Health and settings">
        <p>
          <Term>GET /health/live</Term> answers while the process runs, and{" "}
          <Term>GET /health/ready</Term> answers when the gateway can serve
          requests. The sidebar status dot and the{" "}
          <PageLink to="/">Overview</PageLink> page read the ready check.
          Neither route needs a credential, so a load balancer or an
          orchestrator can probe them directly.
        </p>
        <p>
          Authentication mode, retention windows, and appearance live in{" "}
          <PageLink to="/settings">Settings</PageLink>.
          A local console launch uses a console session.
          Remote readers can use the access page's gateway API key option.
        </p>
      </DocSection>
    </>
  );
}
