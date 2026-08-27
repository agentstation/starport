# PLG9 The gate and the documentation

`scripts/verify-document-parser.sh` reports 20 passed, 0 failed. CI runs it, and
the required evidence list holds it. A gate that no workflow runs cannot report
a regression, so the count above is only worth what the workflow entry is worth.

## What changed

`.github/workflows/ci.yml` runs the gate in the Release Contract job, beside the
other terminal gates. The file `AGENTS.md` adds it to the required evidence
list. It also states the meaning of the gate and its terminal count of 20
conditions.

`AGENTS.md` also gains the concept seam rule for `internal/document`. That
package owns the text layer, the page count, and the scanned verdict, and it
reaches no network address. The engine vocabulary belongs to
`internal/inference`, and the page price comes from the catalog. The rule
records a boundary the tests already hold, so a later change finds the rule
before it finds the test.

The file `docs/OPERATOR-GUIDE.md` gains a Document Parsing section between file
storage and video jobs. It carries the request shape, the two engines and their
costs, and the refusals. It carries the page price and its owner, what the usage
record names, the console page that reads those fields, and the extraction
cache.

`README.md` names the plugin in the version 1 scope list, with the two engines
and what each one charges.

## Why the guide states two prices and one owner

Starmap owns the `documents-recognition` operation and the price of one page.
The guide says so in the section a reader consults about spend. The question
that arrives there is why a document turn cost more than a chat turn.

The section also separates the two page prices this gateway reads. The spend
bound uses the lowest price in the generation, because the planner chooses the
offering after the bound has to decide. The record uses the price of the
offering the planner chose. An operator reading a refusal and a record together
would otherwise see two numbers for one page and find no explanation.

## Evidence

| Command | Result |
| --- | --- |
| `bash scripts/verify-document-parser.sh` | 20 passed, 0 failed |
| `bash scripts/verify-doc-links.sh` | PASS documentation links |
| `bash scripts/test-doc-link-verifier.sh` | PASS edge cases |
| `bash scripts/verify-developer-experience.sh` | 47 passed, 0 failed |
| `bash scripts/verify-readme-quickstart.sh` | PASS |
| `bash scripts/verify-release-workflow.sh` | PASS release workflow contract |
| `bash scripts/verify-action-pins.sh` | 16 references match |
| `go build ./...` | clean |

PLG-V19 and PLG-V20 are this task's own conditions, and both pass. Every
condition in the plan now passes.

## Prose

The three edited documents keep their baseline diagnostic counts under the
technical writing linter. The guide reports 33, `README.md` reports 12, and
`AGENTS.md` reports 11. Each count matches the file before this task, so the new
text adds no diagnostic.
