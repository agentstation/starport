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
