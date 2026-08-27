# PLG4 The recognition path

A scanned document reaches a catalogued recognition offering before the chat
model reads it. The chat route does not move.

## What changed

`internal/proxy/parser.go` holds the orchestration. It reads the engine off
the decoded plugin, extracts every document part in place, and replaces each
one with the text the engine returned. The native engine answers from
`internal/document` and reaches no provider. The recognition engine calls
`RouteDocumentRecognition` when any page arrived scanned.

`internal/router/media.go` no longer writes an empty model into the planning
request. An unnamed model asks the planner for any offering that serves the
operation. That is how a gateway-ordered read stays a catalog question rather
than a table in the router. The file `internal/router/recognition.go` guards
on the document instead of the model for the same reason.

`internal/proxy/proxy.go` computes the key policy and the request metadata
from the request the caller sent, then parses. A document turned into text no
longer looks like a document. A route planned after the rewrite lands on a
text-only model the caller never asked for. Both the completion path and the
stream path carry the same order.

`internal/document/document.go` gained `ErrRecognitionFailed`. It sits beside
the native refusals because a caller reads one document vocabulary.

## Refusal split

A native extraction failure is a `*proxy.ValidationError` and answers 400. The
caller sent bytes the gateway cannot read. A recognition failure is a
`*failure.Failure` of kind `ProviderUnavailable` and answers 503. A short read
counts as a failure. The code compares the returned page count against the
extracted page count and refuses partial text. That is decision PLG-D6.

## Evidence

| Command | Result |
| --- | --- |
| `go test ./...` | exit 0 |
| `go vet ./...` | clean |
| `make lint` | 0 issues |
| `make build` | `Build complete: ./starport` |
| `bash scripts/verify-document-parser.sh` | 11 passed, 9 failed |
| `bash scripts/verify-catalog-driven-providers.sh` | 19 passed, 0 failed |
| `bash scripts/verify-v1-architecture.sh` | 12 passed, 0 failed |
| `bash scripts/verify-dependency-direction.sh` | 6 passed, 0 failed |
| `bash scripts/verify-package-layout.sh` | passed |

PLG-V01 through PLG-V11 pass. PLG-V09, PLG-V10, and PLG-V11 are this task's
own conditions. PLG-V12 through PLG-V20 belong to PLG5 through PLG9.

## Tests

`internal/proxy/parser_test.go` holds eleven tests. Four of them carry the
acceptance:

- `TestAScannedDocumentReachesRecognitionThenTheChatModel` sends
    `scanned.pdf`. It asserts the recognition request carried the bytes and the
  page count. It then asserts the recognized text reached the chat provider.
- `TestThePluginDoesNotMoveTheChatRoute` plans the same request with and
  without the plugin and compares the metadata. The modality list still names
  the document after parsing.
- `TestARecognitionRouteFailureNamesTheStep` asserts the typed error names the
  recognition step.
- `TestAShortRecognitionFailsTheWholeTurn` returns one page for a document
  that holds more and asserts the turn fails.

`internal/routing/parser_plugin_test.go` holds PLG-V10 structurally. A
reflection walk over `routing.Request` asserts no field can carry a parser
engine. A future change that wanted to route on one must add a field, and the
new field fails the test. The second test shows the hazard: the same request
plans to a different model when the document modality is gone.

The tests read fixtures across the package boundary from
`internal/document/testdata`. A PDF records byte offsets, so a second copy is
a second thing that can drift. The digest guard in `internal/document` covers
the originals alone.
