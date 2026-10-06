# Starport site

This package builds the public Starport site as static files. The path `/` is
the splash page. The path `/docs` is the documentation. Fumadocs renders the
documentation from `docs/site/` in place. The package does not copy the
Markdown.

A Cloudflare Worker serves the files in `out/` as static assets. The Worker
runs no code.

## Commands

Run each command in this directory. Use pnpm 11.22.0 and Node.js 24.

| Command | Result |
| --- | --- |
| `pnpm install` | Installs the dependencies and generates `.source/`. |
| `pnpm dev` | Starts a local server at `http://localhost:3000`. |
| `pnpm lint` | Runs ESLint and the TypeScript check. |
| `pnpm test` | Runs the Vitest tests. One test builds the site. |
| `pnpm build` | Writes the static site and the manifest to `out/`. |

The build makes no network request. It reads these repository files:

- `docs/site/` for the pages.
- `console/src/docs/areas.ts` for the sidebar areas and their order.
- `README.md` and `docs/assets/first-use-v1.2.0/` for the splash page.
- `go.mod` for the Starmap module version.

## Manifest

`pnpm build` writes `out/docs/manifest.json`. The manifest has these keys:

| Key | Value |
| --- | --- |
| `starport_release` | The release name, or `dev`. |
| `starmap_module_version` | The Starmap version that `go.mod` requires. |
| `content_revision` | The sha256 of the `docs/site/` tree. |
| `generated_at` | The build time in ISO 8601 format. |
| `files` | The sha256 of each file in `out/`, except the manifest. |

The release name comes from `STARPORT_DOCS_RELEASE`. When this variable is
empty, a GitHub Actions tag build uses its tag. Every other build uses `dev`.
`SOURCE_DATE_EPOCH` sets the build time. The default is the commit time.

The in-binary console writes a docs archive with the same key. For the same
`docs/site/` tree, the two values are equal. `scripts/release-facts.mjs` repeats
the console calculation. `test/release-facts.test.ts` fails when the two
calculations differ.

## Links

The build changes each relative Markdown link:

- A link to a page in `docs/site/` becomes a `/docs` URL with its fragment.
- A link to another repository file becomes a GitHub URL. A release build
  pins the release tag. A dev build pins the commit.
- A link to a missing file stops the build.

## Deploy

The `Site` workflow in `.github/workflows/site.yaml` has two jobs:

1. The `Site` job runs for pull requests and pushes to `main`. It runs the
   checks, builds the site, and uploads `out/` as an artifact.
2. The `Deploy` job runs only when a maintainer starts the workflow by hand.
   The maintainer gives a git reference. The job builds the site at that
   reference. Then it runs `wrangler deploy` in the `starport-site`
   environment.

The environment must have the secrets `CLOUDFLARE_API_TOKEN` and
`CLOUDFLARE_ACCOUNT_ID`. To roll back, start the workflow again with an
earlier reference.

`wrangler.jsonc` binds the Worker to the custom domain
`starport.agentstation.ai`. The first deploy creates the DNS record and the
certificate in the `agentstation.ai` zone. The API token needs the Workers
Scripts edit permission for the account and the DNS edit and Workers Routes
edit permissions for the zone.
