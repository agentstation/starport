# PLG7 The end of a wrong report

Starport reported the `plugins` field as unkept. It runs `file-parser` now, so
that report told a caller its document was never read. The field no longer
appears in the unenforced list.

## What changed

The file `internal/protocol/openrouter/provider_prefs.go` drops the plugins arm
from `unenforcedGatewayFields`. The `transforms` arm stays, because that field
still names work this gateway does not do.

One arm is the whole change. The decode path already refuses every plugin
identifier outside the enforced set, so a request that reaches the report with
plugins named work that ran. No condition guards the arm, because no request can
carry an unenforced plugin this far.

## Why a wrong record is worse than a missing one

A caller reads `X-Starport-Unenforced-Provider-Fields` to learn what did not
happen. The header exists so a request that a gateway cannot fully serve still
answers, and still tells the caller which promise it broke.

A `file-parser` request kept the promise. Naming the field there sends a caller
looking for a fault that is not there. It also hides the real signal. A header that always names one field teaches a caller to stop reading it.

## A deviation from the plan text

The plan asks for a test that an unknown plugin identifier still reports as
unenforced. No such report exists any more.

PLG1 made an unknown identifier a refusal. A plugin changes what the model
reads, so serving the request without it answers a different question and bills
the caller for the answer. A refusal costs the caller one request and names the
enforced set, which the header never did.

The test asserts the refusal instead. `TestAnUnknownPluginIsRefusedRatherThanReported`
records the reason beside the assertion.

## What did not move

Decision PLG-D5 keeps the parity surface closed. This task changed no line of `scripts/verify-openrouter-parity.sh`, and the gate still reports 16 conditions. The three SDK smoke checks pass.

The `transforms` field keeps the behavior invariant P7 protects. It reports as
unkept, it reaches no provider, and it travels in no extension.

## Evidence

| Command | Result |
| --- | --- |
| `go test ./...` | exit 0 |
| `go vet ./...` | clean |
| `make lint` | 0 issues |
| `make build` | `Build complete: ./starport` |
| `go test ./internal/protocol/openrouter/... ./internal/server/...` | ok, ok, ok |
| `bash scripts/verify-openrouter-parity.sh` | 16 passed, 0 failed |
| `bash scripts/smoke-openrouter-sdks.sh` | PASS Python, PASS TypeScript, PASS Go |
| `bash scripts/verify-document-parser.sh` | 17 passed, 3 failed |

PLG-V16 and PLG-V17 are this task's own conditions, and both pass. PLG-V18
through PLG-V20 belong to PLG8 and PLG9.

## Tests

The file `internal/protocol/openrouter/provider_prefs_test.go` replaces the
fail-before assertion with three tests:

- A `file-parser` request names no unenforced field, and the parser it named
  reached the request.
- An unknown identifier draws `ErrUnenforcedPlugin`, and the refusal names the
  enforced plugin.
- A request carrying both fields reports `transforms` alone.

The file `internal/server/controllers/chat_test.go` holds the same contract at
the header. A `file-parser` request beside an unkept provider field reports that
field alone. A request carrying `file-parser` and `transforms` reports
`transforms`. An unknown plugin still draws 400 with the enforced name in the
body.
