# SVA2 canonical inference proof

Date: 2026-08-03
Status: done

## Fail-before

The first canonical contract test failed to compile because `internal/inference` had no owned request, response, content, usage, embedding, or stream values.

```text
internal/inference/contract_test.go:11:13: undefined: ChatRequest
internal/inference/contract_test.go:14:15: undefined: Message
internal/inference/contract_test.go:16:11: undefined: RoleUser
```

The contract inventory also found that the current response-format conversion retains only the format type. It cannot retain a JSON schema, name, description, or strict mode.

## Change

- `internal/inference` owns transport-neutral chat, embedding, multimodal content, tool, structured-output, reasoning, usage, and typed stream values.
- Clone contracts make every slice, map, pointer, image, schema, and nested token list independent at a seam.
- `internal/failure` separates stable kinds and safe messages from provider diagnostics and internal causes.
- The connector adapter converts provider errors into the normalized failure seam.
- The proxy converts HTTP-facing chat and embedding values through the canonical seam.
- The provider adapter preserves multimodal content, tools, JSON Schema output, reasoning, usage, and provider extensions.
- The stream boundary emits typed start, delta, end, and usage events.
- The HTTP controller converts canonical events to OpenAI-compatible SSE chunks at the transport boundary.
- The HTTP failure adapter exposes only safe failure messages and stable error classes.

## Evidence

These commands passed:

```bash
go test ./internal/inference ./internal/failure ./internal/providers/connectors
go test ./internal/inference ./internal/failure ./internal/proxy ./internal/providers/connectors
go test ./internal/inference -run '^$' -fuzz '^FuzzCanonicalInference$' -fuzztime=10s
go test -race ./internal/inference ./internal/failure ./internal/proxy ./internal/providers/connectors
go test ./...
```

The 10-second fuzz gate ran 4,780,319 executions. The four task packages contain 122 top-level test functions and one fuzz target.

The technical-writing linter reported zero diagnostics for 14 changed source and test paths. `git diff --check` also passed.

The architecture verifier now reports:

```text
PASS V02 canonical inference contract
PASS V06 provider credential schema and migration contract
PASS V12 full Go test suite
Summary: 3 passed, 9 failed
```

The remaining verifier failures belong to SVA3 through SVA10. They do not contradict the SVA2 acceptance criteria.
