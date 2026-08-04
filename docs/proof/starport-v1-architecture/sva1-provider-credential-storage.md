# SVA1 provider credential storage proof

Date: 2026-08-03
Status: done

## Fail-before

Command:

```bash
go test ./internal/providers/byok -run '^TestListGlobalKeysReadsCanonicalRecord$' -count=1
```

Result: expected failure. `AddGlobalKey` wrote the canonical record, and `ListGlobalKeys` returned zero records.

```text
--- FAIL: TestListGlobalKeysReadsCanonicalRecord
Error: "[]" should have 1 item, but has 0
```

## Change

`internal/credentials` now owns provider-credential durable identity. SVA6 superseded the first key name with the direct versioned namespace `credentials:v1:`.

The credentials repository uses that namespace for exact reads, scans, writes, and deletes. The BYOK manager uses the repository contract. The generic storage package does not construct provider-credential keys.

The first SVA1 implementation included a compatibility read path. The owner then confirmed that Starport has no released durable-data contract and selected a direct breaking change. This amendment removed the second namespace, multi-key reads, multi-prefix scans, deduplication, and cleanup writes.

The architecture verifier requires all four versioned concept repository contracts. It also rejects all removed namespaces in internal Go files.

## Verification

The named acceptance tests passed. The repository contract covers the canonical key, scan, read, update, and delete lifecycle.

```bash
go test ./internal/providers/byok -run '^(TestListGlobalKeysReadsCanonicalRecord|TestProviderCredentialRepositoryContract)$' -count=1
```

The task package gate passed:

```bash
go test ./internal/providers/byok ./internal/storage ./internal/credentials
```

The race gate passed:

```bash
go test -race ./internal/providers/byok
```

The amended architecture verifier stayed red as required for the active plan. V06 and the full-suite V12 condition stayed green.

```text
PASS V01 Starmap module and Go floor
PASS V02 canonical inference contract
PASS V03 routable snapshot generation contract
PASS V04 deterministic route planner contract
FAIL V05 attempt state and retry budget contract
PASS V06 provider credential canonical schema contract
FAIL V07 response cache semantic identity contract
FAIL V08 production composition fail-closed contract
FAIL V09 public package boundary contract
FAIL V10 OpenRouter protocol contract
PASS V11 import graph architecture fitness
PASS V12 full Go test suite
Summary: 7 passed, 5 failed
```

The focused packages, BYOK race gate, contract gate, and full Go suite passed. The technical-writing linter reported zero diagnostics for the plan and proof. `git diff --check` passed.

## Worktree attribution

SVA1 changed only the provider-credential seam, its tests, verifier condition V06, this proof, and the plan ledger. It preserved the pre-existing provider alias change in `provider_keys.go`.
