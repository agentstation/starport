# ENR-D1 proof: the /v1/responses surface

Date: 2026-08-30. Branch: `codex/enr-d1`.

## What shipped

- `internal/protocol/openai/responses.go` owns the Responses codec.
  Its `DecodeResponses` maps a strict wire request onto the canonical
  chat request, and `EncodeResponses` builds the response envelope.
  The stream encoder folds the canonical stream into the named event
  sequence. The sequence ends with `response.completed` and carries
  no `[DONE]` marker.
- The decoder accepts string input and item-array input: messages,
  function calls, and function call outputs. It maps instructions onto
  a prepended system message. Flat tools and the named tool choice map
  onto the canonical shapes. The `text.format` field maps onto
  structured output, and `max_output_tokens` maps onto the sampling
  bound.
- Requests ride the chat pipeline, so routing, budgets, caching, and
  usage capture treat a Responses call as a chat call.
- A stored-state feature draws a 400 with its parameter name:
  `previous_response_id`, `store=true`, and built-in tool types.
  `UnsupportedError` carries the parameter to the controller.
- `internal/server/controllers/responses.go` mounts the surface, and
  `internal/server/routes.go` serves `POST /v1/responses` behind the
  `chat:write` scope.
- The SDK smoke suite grew an official OpenAI SDK client,
  `scripts/smoke_openai_responses.py`. Raw checks cover the non-stream
  shape, the stream terminator, and the named refusal.

## Acceptance evidence

- `bash scripts/verify-enterprise-readiness.sh`: `Summary: 14 passed,
  19 failed`. ENR-V13 and ENR-V14 turned green, the exact D1 pair.
- ENR-V13 holds the route on the canonical chat contract. ENR-V14
  holds the codec round-trip through its tests.
- `go test ./internal/protocol/openai/`: PASS, nine codec tests cover
  input mapping, tool results, structured output, named refusals,
  the response envelope, and both stream sequences.
- `go test ./internal/server/controllers/`: PASS, three controller
  tests cover the contract, the named 400, and the stream.
- `bash scripts/smoke-openrouter-sdks.sh`: PASS. The official OpenAI
  Python SDK (`openai==3.6.0`) passes non-stream and stream against
  the fixture server, beside the existing OpenRouter SDK checks.

## Commands

- `go test ./...`: PASS.
- `go vet ./...`: PASS. `make lint`: 0 issues. `make build`: PASS.
- `bash scripts/benchmark-overhead.sh`: PASS.
- `bash scripts/smoke-first-run.sh`: PASS.
- The full `verify-*.sh` battery from the required evidence list:
  all 24 structural gates PASS.

## Scope notes

- The surface is the stateless subset. Stored conversations, built-in
  tools, and background mode belong to a response store this gateway
  does not keep. Each refusal says so with the parameter name.
- OpenRouter publishes no Responses route, so `/api/v1` gains none.
- The stream encoder numbers every event and marshals through sorted
  keys, so the wire order stays deterministic under test.
- ENR-D3 reuses `DecodeResponses` through its `io.Reader` contract for
  batch line validation.
