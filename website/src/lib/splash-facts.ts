// The splash page renders the facts in this module and no other facts. Each
// code block and table names its source file, and test/splash.test.ts checks
// that the block is a verbatim part of that file. Thus the page cannot drift
// from the README or the documentation. Prose marks a machine value with
// backticks, and the page renders that value in mono.
//
// This module has no Node import, so the canvas world in the browser can read
// the same facts as the page. src/lib/splash.ts adds the facts that it reads
// from the repository files at build time.

export const README = 'README.md';

export const HEADLINE = ['Own the gateway.', 'Keep your SDK.', 'Trust the catalog.', 'Run one binary.'];

export type InstallMethod = { id: string; title: string; note: string; command: string };

export const INSTALL_METHODS: InstallMethod[] = [
  {
    id: 'homebrew',
    title: 'Homebrew',
    note: 'macOS and Linux',
    command: `brew trust --cask agentstation/tap/starport
brew install --cask agentstation/tap/starport
starport --version`,
  },
  {
    id: 'container',
    title: 'Container image',
    note: 'Versioned image · GitHub attestation',
    command: `STARPORT_VERSION="$(gh release view \\
  --repo agentstation/starport \\
  --json tagName \\
  --jq '.tagName | ltrimstr("v")')"
docker pull "ghcr.io/agentstation/starport:$STARPORT_VERSION"
gh attestation verify "oci://ghcr.io/agentstation/starport:$STARPORT_VERSION" \\
  --repo agentstation/starport \\
  --signer-workflow agentstation/starport/.github/workflows/release.yaml
docker run --rm "ghcr.io/agentstation/starport:$STARPORT_VERSION" --version`,
  },
  {
    id: 'source',
    title: 'Source',
    note: 'Go 1.27.2 · pnpm 11.22.0',
    command: `git clone https://github.com/agentstation/starport.git
cd starport
make build
./starport --version`,
  },
  {
    id: 'quick-start',
    title: 'Quick start',
    note: 'Temporary gateway · no state after it stops',
    command: `unset STARPORT_CATALOG_STATE_DIR STARPORT_FILES_BACKEND
export OPENAI_API_KEY="replace-with-provider-inference-key"
starport dev`,
  },
];

// The final beat of the journey shows the persistent commands of the laptop
// chapter and the Compose commands of the server chapter as tabs beside the
// install methods. The persistent note restates the persistent local gateway
// guide, and the Compose note restates the README sentence about the default
// Compose file.
export const PERSISTENT_METHOD = { id: 'persistent', title: 'Persistent', note: 'One process · Badger · SQLite · files' };
export const COMPOSE_METHOD = { id: 'compose', title: 'Compose', note: 'Default Compose file · one process' };

// The parts of the world that the journey draws, from the app on the left to
// the providers on the right, and the parts beside the route. The title is
// the short name on the card, the label is the line under it, and the
// details are the chips on the card. The card is one paragraph that the
// pointer or a tap opens over the hero still.
export type WorldPartId =
  | 'app'
  | 'listener'
  | 'keys'
  | 'catalog'
  | 'planning'
  | 'providers'
  | 'stream'
  | 'state'
  | 'console'
  | 'controls'
  | 'host';
export type WorldPart = { id: WorldPartId; title: string; label: string; details: string[]; card: string };

export const WORLD_PARTS: WorldPart[] = [
  {
    id: 'app',
    title: 'Your app',
    label: 'OpenAI or OpenRouter client',
    details: ['One base URL', '`STARPORT_API_KEY`', '`chat.completions.create`'],
    card: 'Your OpenAI or OpenRouter client keeps its request and response types. Change its base URL, and use a Starport gateway API key as its API key.',
  },
  {
    id: 'listener',
    title: 'Listener',
    label: 'Two API families',
    details: ['`/v1`', '`/api/v1`'],
    card: 'One binary serves the OpenAI-compatible API at `/v1` and the OpenRouter-compatible API at `/api/v1`. The compatibility boundary is the tested routes and SDK versions.',
  },
  {
    id: 'keys',
    title: 'Key check',
    label: 'Gateway API key',
    details: ['`chat:write`', '`models:read`', 'Budgets', 'Rate limits'],
    card: 'A gateway API key authenticates a client to Starport. It carries the scopes and limits of the client. A gateway API key never pays a provider.',
  },
  {
    id: 'catalog',
    title: 'Catalog',
    label: 'Starmap catalog generation',
    details: ['Candidate', 'Validated', 'Head'],
    card: 'A source supplies a catalog generation. Starport validates the candidate and accepts it as the head. The generation gives every provider, model, capability, context, and price fact.',
  },
  {
    id: 'planning',
    title: 'Planning',
    label: 'Route planning',
    details: ['`openai/gpt-4o-mini`', '`routable`', '`unroutable`'],
    card: 'Route planning selects the offerings that a request can reach. It gives each offering in the generation one verdict.',
  },
  {
    id: 'providers',
    title: 'Providers',
    label: 'One provider inference credential each',
    details: ['`OPENAI_API_KEY`'],
    card: 'A provider inference credential pays a provider for inference. The first provider request proves whether the provider accepts the credential. Starport records the failures in its scoped provider state.',
  },
  {
    id: 'stream',
    title: 'Stream',
    label: 'Server-sent events',
    details: ['`data:`', '`data: [DONE]`'],
    card: 'Starport streams the answer as server-sent events. Each `data:` line carries a chat completion chunk with part of the answer. The last line is `data: [DONE]`.',
  },
  {
    id: 'state',
    title: 'State',
    label: 'Durable state · KV · SQL · blob',
    details: ['KV', 'SQL', 'Blob'],
    card: 'Starport keeps durable state in three roles: KV, SQL, and blob. The local recipe uses Badger, SQLite, and the file system. The shared recipe uses Valkey, PostgreSQL, and object storage. A cache never holds durable state.',
  },
  {
    id: 'console',
    title: 'Console',
    label: 'Embedded web console',
    details: ['One-time launch link', '`starport ui`', '`starport auth token --copy`'],
    card: 'The gateway spends the one-time launch link on first use and exchanges it for a browser session. You paste nothing into the browser. `starport ui` opens a new link at any time.',
  },
  {
    id: 'controls',
    title: 'Controls',
    label: 'Enterprise controls',
    details: ['BYOK policy', 'Secret references', '`/api/v1/activity`', '`/metrics`'],
    card: 'An enterprise adds shared provider inference credentials, BYOK policy, budgets, rate limits, encrypted credential storage, and secret references. Team budgets refuse a request before the provider call.',
  },
  {
    id: 'host',
    title: 'Host',
    label: 'Your host',
    details: ['macOS · Linux · Windows', 'Checksummed archives', 'Attested image'],
    card: 'Supported targets are macOS on Apple silicon, Linux on x86-64 and ARM64, and Windows on x86-64 and ARM64. The current public release contains checksummed archives. The container image carries a GitHub attestation.',
  },
];

// The provider cards in the world. The README names these transport
// primitives, and the catalog gives each provider its credential profile.
export const WORLD_PROVIDERS = ['OpenAI', 'Anthropic', 'Google AI Studio', 'Ollama'];

// The request states that the stage shows as the token travels. The
// timeline places each one on the scroll.
export const REQUEST_STATES = {
  queued: 'Queued · `chat.completions.create`',
  received: 'Received · `/v1`',
  authorized: 'Authorized · `chat:write`',
  resolved: 'Resolved · head generation',
  planned: 'Planned · `openai/gpt-4o-mini`',
  streaming: 'Streaming · server-sent events',
  done: 'Done · `data: [DONE]`',
  console: 'Console · one-time launch link',
  logged: 'Logged · `/api/v1/activity`',
  stored: 'Stored · KV · SQL · blob',
  served: 'Served · one process on your host',
  balanced: 'Balanced · one replica of the fleet',
  temporary: 'Recorded · in-memory state',
};

// The labels that the world draws beside its parts: the catalog source, the
// credential on each lane, the stream back, the server, the fleet, and the
// laptop. The server holds the one process of the README Compose file and
// its three named volumes: the T2 storage recipe on durable volumes, and one
// active gateway (targets.md T3).
export const WORLD_LABELS = {
  binary: 'Starport · one binary',
  source: 'Catalog source',
  cache: 'Cache · disposable',
  cacheNote: 'A cache never holds durable state.',
  local: 'Local recipe',
  shared: 'Shared recipe',
  stream: 'Server-sent events',
  chunk: '`data:`',
  last: '`data: [DONE]`',
  server: 'Your server',
  gateway: 'One active gateway',
  volumes: ['Badger', 'SQLite', 'Files'],
  volumesNote: 'Three named volumes',
  clients: 'Clients',
  balancer: 'Load balancer',
  replicas: ['Starport 1', 'Starport 2', 'Starport N'],
  lease: 'Refresh lease',
  follower: 'Accepted head',
  stores: ['Valkey', 'PostgreSQL', 'Object store'],
  region: 'One region',
  laptop: 'Your laptop',
  dev: '`starport dev`',
  process: 'Starport · one process',
  memory: ['In-memory Badger', 'In-memory SQLite'],
};

// The hosts under the production server: examples of the host you own, not
// qualified targets. The README Compose block runs on any of them, and the
// docs name no tested cloud. On-premises also covers the restricted or
// air-gapped installation of targets.md T6, where no host inside the
// boundary reaches GitHub. Each name is plain text in our own type, and each
// glyph is our own outline, a cloud or a rack, never a brand mark.
export type HostGlyph = 'cloud' | 'rack';
export const HOSTING: {
  caption: string;
  hosts: { name: string; glyph: HostGlyph; note?: string }[];
  sources: string[];
} = {
  caption: 'One Compose file on the host you own: a cloud VM or your own rack.',
  hosts: [
    { name: 'AWS', glyph: 'cloud' },
    { name: 'Google Cloud', glyph: 'cloud' },
    { name: 'Azure', glyph: 'cloud' },
    { name: 'On-premises', glyph: 'rack', note: 'Restricted or air-gapped' },
  ],
  sources: [README, 'docs/site/architecture/targets.md'],
};

// The repository files that the world parts and the request states restate.
export const WORLD_SOURCES = [
  README,
  'docs/site/start/keys-and-roles.md',
  'docs/site/catalog-lifecycle/index.md',
  'docs/site/catalog-lifecycle/routability.md',
  'docs/site/architecture/storage-selection.md',
  'docs/site/architecture/targets.md',
  'docs/site/architecture/topologies.md',
];

// A code block on the page. A block with `verbatim: 'block'` is one
// contiguous part of its source file. A block with `verbatim: 'lines'`
// collects whole lines from the code blocks of its source file. A block with
// `verbatim: 'spans'` collects commands that its source file gives as code
// spans in prose.
export type CodeBlock = {
  kind: 'code';
  lang: 'shell' | 'python' | 'text';
  title?: string;
  source: string;
  verbatim: 'block' | 'lines' | 'spans';
  text: string;
};

// A table on the page. Each row is a verbatim row of a Markdown table in its
// source file. The columns in `mono` are code spans in the source.
export type TableBlock = {
  kind: 'table';
  source: string;
  head: string[];
  rows: string[][];
  mono: number[];
};

// The status of a deployment target, as the target table states it.
export type TargetStatus = { badge: string; target: string; text: string; source: string };

export type Chapter = {
  id: string;
  eyebrow: string;
  claim: string;
  body: string;
  chips: [string, string, string];
  visuals: (CodeBlock | TableBlock)[];
  status?: TargetStatus;
  // The chapter shows the README demo poster under its code.
  demo?: boolean;
};

export const CHAPTERS: Chapter[] = [
  {
    id: 'sdk',
    eyebrow: 'Connect a client',
    claim: 'Keep the SDK. Change one base URL.',
    body: 'A startup keeps its OpenAI and OpenRouter clients and changes one base URL. The client uses a Starport gateway API key as its API key. The request and response types do not change.',
    chips: ['OpenAI `/v1`', 'OpenRouter `/api/v1`', 'One gateway API key'],
    visuals: [
      {
        kind: 'code',
        lang: 'python',
        source: README,
        verbatim: 'block',
        text: `import os

from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8080/v1",
    api_key=os.environ["STARPORT_API_KEY"],
)

response = client.chat.completions.create(
    model="openai/gpt-4o-mini",
    messages=[{"role": "user", "content": "Hello"}],
)`,
      },
    ],
  },
  {
    id: 'surfaces',
    eyebrow: 'API surfaces',
    claim: 'Two API families on one listener.',
    body: 'Starport serves the OpenAI-compatible API at `/v1` and the OpenRouter-compatible API at `/api/v1`. The compatibility boundary is the tested routes and SDK versions. Starport does not yet promise a compatibility window.',
    chips: ['Tested routes', 'Tested SDK versions', 'No provider key as a gateway key'],
    visuals: [
      {
        kind: 'table',
        source: README,
        head: ['Client contract', 'Base URL'],
        rows: [
          ['OpenAI', 'http://127.0.0.1:8080/v1'],
          ['OpenRouter', 'http://127.0.0.1:8080/api/v1'],
        ],
        mono: [1],
      },
    ],
  },
  {
    id: 'credentials',
    eyebrow: 'Credential roles',
    claim: 'Three credential roles. None of them interchangeable.',
    body: 'A gateway API key authenticates a client to Starport and carries its scopes and limits. A provider inference credential pays a provider for inference. A catalog-acquisition credential reads a private catalog source and never pays a provider.',
    chips: ['Gateway API key', 'Provider inference credential', 'Catalog-acquisition credential'],
    visuals: [
      {
        kind: 'code',
        lang: 'shell',
        source: README,
        verbatim: 'lines',
        text: `export STARPORT_API_KEY="replace-with-generated-gateway-key"
export OPENAI_API_KEY="replace-with-provider-inference-key"
export STARPORT_CATALOG_SOURCE_API_KEY="replace-if-the-server-requires-one"`,
      },
    ],
  },
  {
    id: 'catalog',
    eyebrow: 'Catalog',
    claim: 'The catalog is a fact source, not a promise.',
    body: 'These commands read the embedded catalog. They need no credential or network access. Catalog presence does not prove that a provider will accept an inference request.',
    chips: ['No credential', 'No network', 'Membership is not callability'],
    visuals: [
      {
        kind: 'code',
        lang: 'shell',
        source: README,
        verbatim: 'block',
        text: `starport models search gpt-4o --json
starport models show openai/gpt-4o-mini --json`,
      },
    ],
  },
  {
    id: 'first-request',
    eyebrow: 'First request',
    claim: 'A first request streams.',
    body: 'Starport streams the answer as server-sent events, and the last line is `data: [DONE]`. The first provider request proves whether the provider accepts the credential. Starport records authentication, permission, quota, billing, rate-limit, and service failures.',
    chips: ['Server-sent events', '`data: [DONE]`', 'Provider failures recorded'],
    visuals: [
      {
        kind: 'code',
        lang: 'shell',
        source: README,
        verbatim: 'block',
        text: `curl --no-buffer --fail-with-body \\
  -H "Authorization: Bearer $STARPORT_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"openai/gpt-4o-mini","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"Hello"}]}' \\
  http://127.0.0.1:8080/api/v1/chat/completions`,
      },
    ],
    demo: true,
  },
  {
    id: 'console',
    eyebrow: 'Console',
    claim: 'Open the console without pasting a secret.',
    body: 'The `starport ui` command opens the console with a one-time launch link at any time. The gateway spends the link on first use and exchanges it for a browser session. You paste nothing into the browser.',
    chips: ['One-time launch link', '`starport ui`', '`starport auth token --copy`'],
    visuals: [
      {
        kind: 'code',
        lang: 'text',
        source: README,
        verbatim: 'block',
        text: `Starport development gateway
URL: http://127.0.0.1:8080
Authentication: required
Gateway API key (shown once): replace-with-generated-gateway-key
Console (one-time launch link): http://127.0.0.1:8080/launch?lt=replace-with-ticket`,
      },
    ],
  },
  {
    id: 'enterprise',
    eyebrow: 'Enterprise',
    claim: 'Enterprise controls live in the gateway.',
    body: 'An enterprise adds shared provider inference credentials, BYOK policy, budgets, rate limits, encrypted credential storage, and secret references. Run `starport config show` to see the effective values without secrets. Run `starport config validate` to check the same configuration that `starport serve` loads.',
    chips: ['BYOK policy', 'Budgets and rate limits', 'Secret references'],
    visuals: [
      {
        kind: 'code',
        lang: 'shell',
        source: 'docs/site/configure/index.md',
        verbatim: 'spans',
        text: `starport config show
starport config validate`,
      },
    ],
  },
  {
    id: 'storage',
    eyebrow: 'Storage',
    claim: 'Pick the storage recipe for the process count.',
    body: 'Starport keeps durable state in three roles: KV, SQL, and blob. The local recipe uses Badger, SQLite, and the file system. The shared recipe uses Valkey, PostgreSQL, and object storage.',
    chips: ['Badger · SQLite · files', 'Valkey · PostgreSQL · object storage', 'A cache never holds durable state'],
    visuals: [
      {
        kind: 'table',
        source: 'docs/site/architecture/storage-selection.md',
        head: ['Target', 'KV', 'Relational', 'File bytes'],
        rows: [
          ['T7 development', 'In-memory Badger', 'In-memory SQLite', 'Scratch directory'],
          ['T2 persistent local', 'Badger', 'SQLite', 'Local directory'],
          ['T3 one production server', 'Badger on a durable volume', 'SQLite on a durable volume', 'Local directory on a durable volume'],
          ['T4 replicated', 'Valkey', 'PostgreSQL', 'Object store'],
        ],
        mono: [],
      },
    ],
  },
  {
    id: 'server',
    eyebrow: 'Production server',
    claim: 'Deploy to the host you own.',
    body: 'The default Compose file builds one Starport process with persistent Badger, SQLite, and file storage. The Container image install tab verifies the GitHub attestation of the image. Several replicas need the replicated recipe.',
    chips: ['macOS · Linux · Windows', 'Attested image', 'AGPLv3'],
    visuals: [
      {
        kind: 'code',
        lang: 'shell',
        source: README,
        verbatim: 'block',
        text: `cp .env.example .env
chmod 600 .env
# Edit .env. Set STARPORT_SECURITY_MASTER_KEY and OPENAI_API_KEY.
docker compose build starport
docker compose run --rm starport init --configured-storage --name primary-admin
docker compose run --rm starport auth rotate
docker compose up -d starport`,
      },
    ],
    status: {
      badge: 'Qualification open',
      target: 'T3 one production server',
      text: 'Available. Single-process recovery tested. Production qualification open.',
      source: 'docs/site/architecture/targets.md',
    },
  },
  {
    id: 'scale-out',
    eyebrow: 'Scale out',
    claim: 'Scale out behind one load balancer.',
    body: 'Several Starport replicas run behind one load balancer and share one Valkey service, one PostgreSQL database, and one object store. One replica at a time holds the refresh lease and owns provider acquisition, and the other replicas follow the shared accepted head. Each fleet runs in one region, and cross-region replication and multi-region failover are out of scope.',
    chips: ['One load balancer', 'One refresh lease', 'One region'],
    visuals: [
      {
        kind: 'code',
        lang: 'text',
        title: 'Every replica',
        source: 'docs/site/operate-starport/fleet.md',
        verbatim: 'block',
        text: `STARPORT_DEPLOYMENT_ID=<deployment-id>
STARPORT_STORAGE_MODE=valkey
STARPORT_STORAGE_SQL_MODE=postgres
STARPORT_FILES_BACKEND=objectstore`,
      },
    ],
    status: {
      badge: 'Failover limits',
      target: 'T4 replicated Starport',
      text: 'Fleet qualified on Valkey 7.2.14 and PostgreSQL 16.15. Failover limits apply.',
      source: 'docs/site/architecture/targets.md',
    },
  },
  {
    id: 'laptop',
    eyebrow: 'On a laptop',
    claim: 'Temporary by default. Persistent by choice.',
    body: 'Homebrew installs Starport on macOS and Linux. The `starport dev` command starts a temporary gateway with in-memory Badger and SQLite, and it removes the gateway state at shutdown. To keep data, run `starport init` once and then `starport serve`.',
    chips: ['Homebrew · macOS · Linux', 'Removed at shutdown', '`starport init` keeps data'],
    visuals: [
      {
        kind: 'code',
        lang: 'shell',
        title: 'Persistent',
        source: 'docs/site/start/local-persistent.md',
        verbatim: 'lines',
        text: `starport init --name primary-admin
starport serve
starport ui`,
      },
    ],
  },
];
