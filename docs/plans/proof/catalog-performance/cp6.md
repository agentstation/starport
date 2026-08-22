# CP6 proof — Starmap v0.7.0 identity payload and Starport pin

## Starmap side (agentstation/starmap PR #95, merged e7867540, released v0.7.0)

- Payload carries `Provider.Description`, `Provider.DocsURL`, `Provider.Logo`,
  `Author.Logo`, `Model.DeprecatedAt`, `Model.RetiresAt`, and offering
  lifecycle dates. Catalog schema version 5 → 6.
- Logo sidecar model: `providers/<id>/logo.svg` and `authors/<id>/logo.svg`
  load into payload bytes and survive SaveTo/NewFromPath round trips.
- Coverage fill: 15/15 providers and 42 authors carry logos (41 interim +
  phind). 15 providers carry descriptions and docs URLs.
- Fail-before: `TestCatalogPayloadCarriesIdentityAndLifecycle` and
  `TestCatalogWorkspaceRoundTripPreservesIdentity` fail on v0.6.0 types.
- New public accessor `starmap.EmbeddedBuilder()` replaces the removed
  `catalogs.NewEmbedded` for consumers after dependency-direction hardening.
- Gate: starmap `make all` green; CI Verification Gate green after three
  regenerations the identity change required (pinned artifact digest,
  embedded OpenAPI specs, gomarkdoc package docs).

## Starport side (this PR)

- `go.mod` pins `github.com/agentstation/starmap v0.7.0`; the temporary
  `../starmap` replace is gone (CPV18, V01 both pass).
- `internal/catalog/view`: `ProviderInfo.DocsURL` added; description and
  docs URL project from the catalog snapshot (CPV04).
- `internal/catalog/view/logos.go`: `view.Logo` projects catalog SVG bytes
  for providers and authors.
- `Proxy.GetLogo` flows through proxy, cache, logging, and timing layers.
- `LogosController` prefers catalog bytes and falls back to the embedded
  interim set. Contract test `TestLogosControllerPrefersCatalogBytes`.

## Acceptance evidence (2026-08-22, local dev gateway on 127.0.0.1:8080)

- `GET /api/v1/providers`: 12/12 configured providers carry `description`
  and `docs_url`.
- `GET /api/v1/logos/authors/phind.svg`: 200, 719 bytes, `image/svg+xml`.
  Phind is absent from the interim embedded set, so the bytes prove the
  catalog-payload path.
- `go test ./...` green; providers projection golden regenerated to pin the
  new fields.
- Gates: verify-starmap-ownership, verify-v1-architecture,
  test-dependency-direction-verifier, verify-dependency-direction,
  verify-package-layout, verify-readme-quickstart, verify-openrouter-parity,
  verify-catalog-driven-providers all PASS; `go vet` clean; `make lint`
  0 issues; `make build` ok; OpenRouter SDK smoke 3/3 PASS.
- verify-catalog-performance.sh: 10 passed, 8 failed — the 8 remaining
  conditions (CPV07–CPV14) belong to CP7–CP17 and stay red until those
  tasks land; the script joins CI at CP18.
