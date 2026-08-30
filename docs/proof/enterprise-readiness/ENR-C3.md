# ENR-C3 proof: security posture document

Date: 2026-08-30. Branch: `codex/enr-c3`.

## What shipped

- `docs/SECURITY-POSTURE.md` answers a security review in one place.
  It covers the credential model, encryption at rest, authentication,
  the audit surface, data flows, and budgets.
- Every claim names the test or the verification gate that proves it.
  The gate table names seven scripts and what each one holds.
- A deployment hardening checklist closes the document: bind address,
  master key, TLS, rotation, CORS, body bounds, metrics access, and
  retention.
- The document stays distinct from `SECURITY.md`, which owns
  vulnerability disclosure. The two documents cross-link.
- Links land in the README documentation list, the README license
  section, the docs index, and the operator guide intro.
- `scripts/verify-doc-links.sh` now lists the document, so every link
  it carries stays resolved.

## Acceptance evidence

- `bash scripts/verify-enterprise-readiness.sh`: `Summary: 12 passed,
  21 failed`. ENR-V12 turned green, the exact phase C target.
- ENR-V12 checks that `SECURITY-POSTURE.md` appears in `README.md`.
  The README carries two such links.
- Technical-writing lint: `docs/SECURITY-POSTURE.md`, `SECURITY.md`,
  and `README.md` pass with zero diagnostics. The one `docs/README.md`
  diagnostic predates this task and sits outside the touched lines.

## Commands

- `bash scripts/verify-doc-links.sh`: PASS.
- `bash scripts/test-doc-link-verifier.sh`: PASS.
- `bash scripts/verify-readme-quickstart.sh`: PASS.
- `bash scripts/verify-enterprise-readiness.sh`: 12 passed, ENR-V01
  through ENR-V12 green.
- No Go source changed, so the compiled gates hold their prior result.

## Scope notes

- The document states the posture and points at proof. It certifies
  nothing itself, and each claim stays refutable by its named test.
- The document states the budget fail-open behavior plainly.
  Availability wins over enforcement on a storage error.
- TLS exposes no cipher or version knob. Go's `crypto/tls` defaults
  apply, and the document says so through the proxy guidance.
