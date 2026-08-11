# Automatic provider runtime proof

This directory stores task evidence for
`docs/plans/automatic-provider-runtime-plan.html`.

The active plan uses Starport `main` commit
`b52cd7e286a9a870293155638392ed514b630a47` as its baseline. APR0 will add the
red verifier output and focused fail-before evidence before production code
changes.

No implementation evidence exists at plan authorship.

## Evidence index

- [`audit.md`](audit.md): source, architecture, dependency, and platform review
  completed before activation.
- [`dependencies.txt`](dependencies.txt): complete direct-module currency output
  and local Go toolchain evidence.
- [`APR0.md`](APR0.md): named red verifier, fail-before output, and preserved
  green baseline controls.
- [`APR1.md`](APR1.md): provider-neutral production bootstrap and isolated
  local development runtime.
- [`APR2.md`](APR2.md): catalog-wide provider registration, request-bound
  endpoint binding, and credential-independent readiness.
- [`APR3.md`](APR3.md): credential-driven startup, interval, and manual
  reconciliation with atomic runtime publication.
- [`APR4.md`](APR4.md): secret-free provider state, evidence-scoped failures,
  and exact material-version recovery.
- [`APR5.md`](APR5.md): authenticated provider status and forced credential
  reconciliation routes.
- [`APR6.md`](APR6.md): verified first-run, automatic provider discovery,
  refresh operations, and provider-neutral container configuration.
