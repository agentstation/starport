# POR2 Starmap catalog release-policy proof

POR2 replaces unclassified numeric release failures with an explicit,
versioned policy.

## Fail-before evidence

The old report could reject a release for five numeric limits. The source and
history did not define an approved operational objective, owner, consequence,
exception path, or reopen condition for those values.

The existing report measured:

| Measurement | Value | Old limit |
|---|---:|---:|
| Generation age | 137,108 seconds | 30 days |
| Canonical payload | 8,110,865 bytes | 16 MiB |
| Compressed artifact | 298,892 bytes | 8 MiB |
| Providers | 14 | At least 5 |
| Models | 590 | At least 100 |

The values passed, but the enforcement contract was incomplete. The provider
and model floors also had no approved catalog-coverage objective.

## Policy disposition

The report schema and policy now use version 1.

| Measurement | Disposition |
|---|---|
| Future generation time | Hard correctness gate |
| Generation age | Non-blocking review threshold |
| Canonical payload size | Non-blocking review threshold |
| Compressed artifact size | Non-blocking review threshold |
| Provider count | Measurement only |
| Model count | Measurement only |

The hard gate defines its objective, measurement method, unit, approved limit,
consequence, owner, exception path, and reopen condition. Policy validation
rejects a missing field before catalog measurement. The command has no
environment bypass.

The change removes the old provider and model minimums. Counts remain in the report.
Catalog validation continues to enforce structural integrity.

## Verification

The focused ordinary and race suites passed:

```text
go test -count=1 ./internal/bootstrap/budget ./cmd/starmap-embedded-budget ./internal/ciworkflow -run '^(TestCatalogBudget|TestEmbeddedBudget|TestReleaseWorkflow)'
go test -race -count=1 ./internal/bootstrap/budget ./cmd/starmap-embedded-budget ./internal/ciworkflow -run '^(TestCatalogBudget|TestEmbeddedBudget|TestReleaseWorkflow)'
```

The focused race times were 46.521 seconds for the budget package, 24.422
seconds for the command, and 1.395 seconds for workflow contracts.

The current command report passes. It records 14 providers, 590 models,
8,110,865 canonical bytes, and 298,892 compressed bytes. It has no policy
finding.

`make embedded-catalog-budget-check`, generated docs, strict technical-writing
lint, and the complete `make verify` gate passed. The full gate included the
uncapped repository race suite, pure-Go consumer compositions, vet, lint,
coverage, catalog validation, and CLI smoke tests.

The campaign verifier now reports:

```text
PASS POR-V01 Starmap approved package layout
PASS POR-V02 Starmap source-payload and bootstrap behavior
PASS POR-V03 Starmap catalog limit policy
Summary: 3 passed, 6 failed
```

POR-V04 through POR-V09 remain red because their owning tasks have not run.

## Review

Commit `c313b19a` passed the isolated pre-PR autoreview with GPT-5.6-sol at
high reasoning. The reviewer reported no accepted or actionable finding and
assigned 0.98 confidence that the patch is correct. Its secret scan passed.

The automatic Claude selection failed before it read the patch because its
client rejected a local self-signed certificate. This is a reviewer transport
failure, not product evidence. The isolated Sol fallback completed the required
review gate.
