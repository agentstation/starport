import { readFileSync } from 'node:fs';
import path from 'node:path';

import { release, starmapVersion } from '../../scripts/release-facts.mjs';

// The splash page renders the facts in this module and no other facts. Each
// code block and table names its source file, and test/splash.test.ts checks
// that the block is a verbatim part of that file. Thus the page cannot drift
// from the README or the documentation. Prose marks a machine value with
// backticks, and the page renders that value in mono.

const repoRoot = path.resolve(process.cwd(), '..');

export const README = 'README.md';

export function readRepoFile(file: string): string {
  return readFileSync(path.join(repoRoot, file), 'utf8').replace(/\r\n/g, '\n');
}

// lede returns the first paragraph of README.md that starts with "Starport
// is", joined into one line.
export function lede(): string {
  const paragraph = readRepoFile(README)
    .split('\n\n')
    .find((block) => block.startsWith('Starport is '));
  if (!paragraph) throw new Error('README.md has no paragraph that starts with "Starport is"');
  return paragraph.replace(/\n/g, ' ');
}

// posterSize reads the width and the height from the PNG header of the demo
// poster. The page uses them to keep the space of the demo before it loads.
export function posterSize(): { width: number; height: number } {
  const png = readFileSync(path.join(repoRoot, 'docs/assets/first-use-v1.2.0/poster.png'));
  return { width: png.readUInt32BE(16), height: png.readUInt32BE(20) };
}

// siteBuild gives the release name and the Starmap module version of this
// build. The manifest reads the same helper.
export function siteBuild(): { release: string; starmap: string } {
  return { release: release(), starmap: starmapVersion() };
}

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
    note: 'Go 1.27.1 · pnpm 11.22.0',
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

// The parts of the request path in the hero scene. The label is the name on
// the drawing. The card is one paragraph that the pointer or the keyboard
// opens.
export type ScenePartId = 'catalog' | 'app' | 'starport' | 'providers' | 'state';
export type ScenePart = { id: ScenePartId; role: string; label: string; details: string[]; card: string };

export const SCENE_PARTS: ScenePart[] = [
  {
    id: 'catalog',
    role: 'Fact source',
    label: 'Starmap catalog generation',
    details: ['Validated · accepted as head'],
    card: 'A source supplies a catalog generation. Starport validates the candidate and accepts it as the head. The generation gives every provider, model, capability, context, and price fact.',
  },
  {
    id: 'app',
    role: 'Client',
    label: 'Your app',
    details: ['OpenAI or OpenRouter SDK', 'Base URL changed', '`STARPORT_API_KEY`'],
    card: 'Your OpenAI or OpenRouter client keeps its request and response types. Change its base URL, and use a Starport gateway API key as its API key. A gateway API key never pays a provider.',
  },
  {
    id: 'starport',
    role: 'Gateway',
    label: 'Starport',
    details: ['`/v1` · `/api/v1`', 'Keys · scopes · budgets · rate limits', 'Route planning'],
    card: 'One binary serves the OpenAI-compatible API at `/v1` and the OpenRouter-compatible API at `/api/v1`. The gateway API key carries the scopes and limits of the client. Route planning selects the offerings that a request can reach.',
  },
  {
    id: 'providers',
    role: 'Upstream',
    label: 'Providers',
    details: ['One provider inference credential each', '`OPENAI_API_KEY`'],
    card: 'A provider inference credential pays a provider for inference. The first provider request proves whether the provider accepts the credential. Starport records the failures in its scoped provider state.',
  },
  {
    id: 'state',
    role: 'Storage',
    label: 'Durable state',
    details: ['KV · SQL · blob', 'Local or shared recipe'],
    card: 'Starport keeps durable state in three roles: KV, SQL, and blob. The local recipe uses Badger, SQLite, and the file system. The shared recipe uses Valkey, PostgreSQL, and object storage.',
  },
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
    id: 'lifetime',
    eyebrow: 'Gateway lifetime',
    claim: 'Temporary by default. Persistent by choice.',
    body: 'The `starport dev` command starts a temporary gateway with in-memory Badger and SQLite. At shutdown, it removes the keys, accounts, sessions, usage, budgets, files, batches, presets, and catalog state. To keep data, run `starport init` once and then `starport serve`.',
    chips: ['In-memory state', 'Removed at shutdown', '`starport init` keeps data'],
    visuals: [
      {
        kind: 'code',
        lang: 'shell',
        title: 'Temporary',
        source: README,
        verbatim: 'block',
        text: `unset STARPORT_CATALOG_STATE_DIR STARPORT_FILES_BACKEND
export OPENAI_API_KEY="replace-with-provider-inference-key"
starport dev`,
      },
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
  {
    id: 'console',
    eyebrow: 'Console',
    claim: 'Open the console without pasting a secret.',
    body: 'The `starport dev` command prints one gateway API key and a one-time console launch link. The gateway spends the link on first use and exchanges it for a browser session. You paste nothing into the browser.',
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
    status: {
      badge: 'Failover limits',
      target: 'T4 replicated',
      text: 'Fleet qualified on Valkey 7.2.14 and PostgreSQL 16.15. Failover limits apply.',
      source: 'docs/site/architecture/targets.md',
    },
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
    id: 'deploy',
    eyebrow: 'Deploy',
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
];
