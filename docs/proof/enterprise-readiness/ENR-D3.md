# ENR-D3 proof: the /v1/batches surface

Date: 2026-08-30. Branch: `codex/enr-d3`.

## What shipped

- `internal/inference/batches.go` owns the canonical vocabulary.
  It defines the create request, the batch record a caller polls,
  the decoded input line, and the per-line result. No shape names
  a provider, because every line routes through the planner and one
  batch may touch many providers.
- `internal/jobs` generalizes beyond video. The file `batch.go`
  owns the batch record, the five canonical states, and the legal
  transitions. The file `batch_repository.go` stores the record
  under the jobs prefix. The service in `batch_service.go` owns
  submission, the JSONL line scanner with its byte bound, the
  bounded dispatch loop, and cancellation. Cancel closes the
  dispatch gate, so no new line starts. In-flight lines drain on a
  background context.
- The service claims an outstanding-work slot before it stores the
  record. It shares the job meter with the video service, so one
  bound governs all background work an account holds open.
- `internal/files` carries the two batch purposes. An upload for
  `batch` feeds a batch. Only the runner writes the `batch_output`
  purpose, and the upload route refuses it by name. The output
  and error files ride the same retention and stored-byte bound as
  every other stored file.
- `internal/protocol/openai/batches.go` owns every wire word. The
  create decoder is strict, validates the endpoint against the
  three request-shaped operations, and accepts only the `24h`
  window. Metadata decodes and is not stored. The status mapping
  renders `queued` as `validating` and `running` as `in_progress`.
  A result line carries the `batch_req_` identifier, the online
  response envelope under `response`, and a null `error` slot.
- `internal/server/controllers/batches.go` mounts the surface on
  the jobs seam. Create resolves the stored input file, refuses a
  wrong purpose, mints the batch identifier, and submits the line
  runner. The runner decodes each line with the online codec,
  forces streaming off, and executes through `ProcessChatCompletion`,
  `ProcessEmbeddings`, or the responses translation. A failed line
  carries the online error envelope with its status code.
- `internal/server/batch_governor.go` admits each line under the
  meters the middleware runs online. Budget checks run first, in
  the online order, and a refusal fails the line with the online
  402 words. A rate refusal waits for the window to reset, because
  no 429 answer can reach a caller. A budget read failure allows
  the line and logs, exactly as the online middleware fails open.
- The routes mount under `/v1/batches` behind the one
  `batches:write` scope: create, list, get, and cancel. The
  anonymous scope set and the console default non-admin key grant
  it. OpenRouter publishes no batch route, so `/api/v1` gains none
  and the parity gate stays at 17.
- Every line writes one usage record through the shared capture,
  and the record carries the batch identifier. The spend joins to
  the batch without a second metering path.

## Acceptance evidence

- `TestBatchRunsToATerminalRecordThroughTheRouter` runs the
  three-line acceptance pass over HTTP: upload, create, poll to
  `completed`, and read three responses back from the output file.
  Each output line carries its `custom_id`, the `batch_req_`
  identifier, and status 200.
- `TestBatchCancelStopsNewLines` in `internal/jobs` holds the
  cancel contract. The batch reaches `cancelled`, no new line
  starts after the gate closes, and in-flight lines drain.
- `TestBatchLineUsageRecordCarriesTheBatchID` in `internal/proxy`
  holds the attribution join: a batched line records the batch
  identifier and an online request records none.
- `bash scripts/verify-enterprise-readiness.sh`: `Summary: 17
  passed, 16 failed`. ENR-V16 and ENR-V17 turned green, the exact
  D3 conditions. The 16 open conditions belong to later phases.
- `go test ./internal/jobs/... -race`: PASS.
- `go test ./internal/server/ ./internal/protocol/openai/
  ./internal/proxy/`: PASS. The route tests hold registration, the
  standalone scope, and the anonymous deployment. They hold the
  missing-file and unknown-identifier 404 answers, the
  wrong-purpose 400, and the post-terminal cancel conflict. They
  hold the reserved-purpose upload refusal. The codec tests hold every wire
  key, the status words, the line rules, and the result line shape.
- Console: `pnpm test` 210 passed across 33 files after the default
  scope change.

## Commands

- `go test ./...`: PASS.
- `go vet ./...`: PASS. `make lint`: 0 issues. `make build`: PASS.
- `bash scripts/verify-async-media-jobs.sh`: 18 passed, terminal.
- `bash scripts/verify-files-api.sh`: 22 passed, terminal.
- The full `verify-*.sh` battery from the required evidence list:
  all structural gates PASS.

## Scope notes

- The completion window is a promise this gateway keeps trivially.
  Work starts at once, so the codec validates `24h` and echoes it.
- A batch with failed lines still completes. The caller's answer
  lives in the output and error files. The request counts on the
  record say how many lines went each way.
- A line over the byte bound fails the whole batch and the record
  names the bound. The scanner skips a blank line.
- Media operations stay online. A result line carries a JSON body,
  not bytes.
