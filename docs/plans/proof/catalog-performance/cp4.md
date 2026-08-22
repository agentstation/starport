# CP4 — Authors API

Date: 2026-08-22. Branch: `codex/cp-4-authors-api`.

## Scope

Expose the catalog's authors through the gateway API.

- `internal/catalog/view/authors.go`: `AuthorInfo` projection (id, name,
  description, headquarters, icon URL, website, GitHub, HuggingFace,
  Twitter, sorted model IDs via `AuthorModels`), `Authors(snapshot)` sorted
  by ID, and `AuthorByID(snapshot, id)`.
- `proxy.Proxy` gains `ListAuthors` and `GetAuthor`; implemented by the
  core proxy (shared `acquireSnapshot` helper), delegated by the cache,
  logging, and timing wrappers; unknown IDs return the `not_found`
  provider error that the controller layer maps to HTTP 404.
- `AuthorsController` with `GET /api/v1/authors` and
  `GET /api/v1/authors/{author}`, registered under the `models:read`
  scope.

## Fail-before evidence

- `curl /api/v1/authors` against the pre-CP4 gateway build returned 404.
- The three view seam tests failed to compile before `authors.go` existed
  (`undefined: Authors`, `undefined: AuthorByID`, `undefined: AuthorInfo`).
- `CPV01` was red before the route registration.

## Gates

- `go test ./...` — ok, zero failures (view seam: 3 new tests; controller
  seam: 3 new tests)
- `go vet ./...` — clean
- `make lint` — 0 issues
- `make build` — ok
- `verify-starmap-ownership.sh` — 12 passed
- `verify-v1-architecture.sh` — 12 passed
- `verify-dependency-direction.sh` — 6 passed
- `verify-package-layout.sh` — passed
- `verify-openrouter-parity.sh` — 16 passed
- `verify-catalog-performance.sh` — 6 passed (CPV01 newly green), 12
  failed as scoped to later tasks

## Live acceptance evidence

Ephemeral dev gateway built from this change (loopback, in-memory,
one-time key, since shut down):

- `GET /api/v1/authors` returned 104 authors — the exact count the plan
  names as invisible before this task.
- The `anthropic` entry carried name, description, headquarters, website,
  GitHub, and 26 model IDs.
- `GET /api/v1/authors/groq` returned the projected detail (4 models).
- `GET /api/v1/authors/no-such-author` returned 404.
