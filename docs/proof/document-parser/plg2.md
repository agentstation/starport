# PLG2 The native engine

## Outcome

`scripts/verify-document-parser.sh` moves from 4 of 20 to 5 of 20. `PLG-V04`
and `PLG-V05` pass, and `PLG-V16` goes back to failing because PLG2 tightened
it. The package `internal/document` now turns a PDF into text inside this
process. It reaches no provider, so a document that already carries a text
layer costs nothing but the read.

## Why a reader had to be chosen rather than written

Two pure Go options exist for reading a PDF text layer. Neither is the obvious
one.

`pdfcpu` is the most active pure Go PDF library. It has no text extraction at
all. Its `extract.go` exports images, fonts, pages, content streams, and
metadata, and nothing that returns text. A caller wanting text has to write a
content stream interpreter on top of it.

`github.com/ledongthuc/pdf` is a fork of `rsc.io/pdf`. It is MIT licensed, pure
Go, and carries no transitive dependency at all. It returns positioned glyph
runs through `Page.Content().Text`, which is what a gap-aware assembler needs.
The zero dependency count is the deciding property. It keeps
`internal/document` a leaf, which invariant P3 requires.

## The reader panics, so every call runs under recovery

The chosen reader signals malformed input by panicking. That is not a guess. A
direct probe against the raw library confirms it:

```
/Count ??  ->  PANIC unexpected keyword "??" parsing object
```

The bytes arriving at this package are a caller's own upload. A gateway that
answered a malformed upload with a crashed request would let one caller decide
whether other callers get served.

So `readPDF` wraps every call into the library in `recovering`, and a recovered
panic becomes `ErrMalformedDocument`. The test
`TestMalformedBytesAreRefusedRatherThanCrashing` covers four malformed inputs
and asserts both `require.NotPanics` and the typed refusal. One of its cases is
the `/Count ??` input above, so the recovery stays load bearing rather than
decorative.

Recovery here cannot hide a test failure. A failed testify assertion raises
`runtime.Goexit`, which `recover` does not catch.

## Three writers, because one writer proves only itself

The acceptance line asks for documents produced by several writers. A single
fixture proves that the engine reads that one writer.

Three real producers on this machine wrote the fixtures:

- `quartz.pdf` from the macOS Quartz PDFContext, through `cupsfilter`.
- `pdftex.pdf` from pdfTeX 1.40.29, through `pdflatex`.
- `handwritten.pdf`, a 616 byte file written by hand.

The three disagree completely about how much layout they encode. Quartz writes
one run per line and includes real space characters. pdfTeX positions every
word separately and writes no space at all. The hand written file uses a single
`Tj` operator.

This task read each fixture for machine identifying content before the commit.
They carry producer strings and dates and nothing else.

## A PDF stores no spaces

Reading the runs in order and joining them returns this for the pdfTeX file:

```
Starportnativeextractionproof.
```

That is a materially different document than the caller attached. A model
reading it reads different words.

The function `assemble` fixes this. It sorts runs by `Y` descending and then by
`X` ascending. It breaks a line when two baselines differ by more than one
point. It writes a space when the horizontal gap exceeds `0.2` times the font
size. The same file now returns:

```
Starport native extraction proof.
```

The test `TestAWordGapBecomesASpace` holds the property. It asserts that the
text contains `The quick brown fox` and does not contain `Thequickbrownfox`.

Page assembly runs per page, because `Y` coordinates repeat on every page. The
function `joinPages` then separates pages with a blank line, so a model reading
the text can tell where one page ended.

## A scanned document is a signal, not a failure

A scanned page carries no text layer. Reporting that as an error would be
wrong, because the scanned result is what routes the page to the recognition
engine in PLG4.

A pure emptiness test fails in the wrong direction. A scanned page often
carries one stray glyph from a watermark or a font encoding artifact. The
pdfTeX fixture emits a stray `Ω` of its own. So `usableText` counts letters and
digits and requires eight before it calls a text layer usable. It counts non
ASCII letters too, so a page in a non Latin script does not read as scanned.

The test `TestAScannedDocumentReportsScannedRatherThanEmptyText` reads a one
page fixture that holds a Flate encoded RGB image and no text operator.

## The two bounds have different owners

The page bound refuses a document before the engine reads any page. The page
count comes from the document catalog, so a thousand page upload costs one
header parse. The test `TestThePageBudgetRefusesBeforeAnyPageIsRead` sets the
bound to five against a twelve page fixture, and asserts both that the refusal
names `12 pages` and that no page came back.

The engine checks the elapsed time bound between pages, because the reader
cannot stop itself.

A caller that cancels and a bound that fires are different failures with
different fixes, so they stay separate. The test
`TestACanceledCallerStopsTheExtraction` asserts `context.Canceled` and asserts
that the answer is not `ErrTimeBudgetExceeded`. The test
`TestTheTimeBudgetIsReportedAsItsOwnBound` asserts the reverse.

## Declared format against read format

A caller names a container. The bytes are the evidence for what the container
really is.

The first version of `Extract` checked the sniff before the caller's claim, so
a PNG declared as `pdf` reported `ErrUnsupportedFormat`. That names the wrong
failure. The caller's own claim is the thing that failed, whether or not this
engine recognizes what the bytes actually are.

The check is now ordered so a non empty declared format that disagrees with the
sniffed format always returns `ErrFormatMismatch`. The test
`TestBytesThatAreNotTheDeclaredFormatAreRefused` covers PNG magic bytes, plain
text, and a valid PDF with junk bytes prepended.

The header sniff reads offset zero and does not hunt deeper in the file. A
reader that hunts accepts files built to look like something else.

## The leaf rule

`internal/architecture/import_graph_test.go` now lists `internal/document` and
asserts two rules. The package imports only internal packages, and it imports
no provider, proxy, registry, router, or server package, and neither `net/http`
nor `net/url`.

The comment in the test names the reason. An import of a provider or a
transport would let a document that carries its own text leave the process
anyway. The caller would then pay for a read this package already did. The
recognition engine reaches the network through a route in PLG4, not through an
import here.

## A correction to the PLG1 record

`PLG-V16` passed at the PLG1 merge commit. It should not have.

The condition claims that a `file-parser` request names no unenforced field.
The condition itself was a grep for two terms. The tests PLG1 re-pointed at
`file-parser` contain both terms while asserting the opposite property, which
is PLG7's fail before state.

A temporary worktree at the merge commit `d11a621` reproduces the count. It
reports `Summary: 4 passed, 16 failed`, with `PLG-V01`, `PLG-V02`, `PLG-V03`,
and `PLG-V16` passing.

PLG2 tightened the condition. The helper `no_unenforced_plugin_report` still
requires the two test terms, and it now also requires that
`internal/protocol/openrouter/provider_prefs.go` no longer lists `"plugins"`.
That second clause is the property. The condition fails again, and it passes
when PLG7 removes the report.

The file `plg1.md` carries a `## Correction` section recording the real count
of 4 of 20 and why that fourth pass was wrong. A verifier is the plan's own
instrument. A condition that passes before its property holds corrupts the
record it exists to keep.

## Evidence

```
go test ./internal/document/                      ok, 24 tests
go test ./internal/architecture/                  ok
bash scripts/verify-document-parser.sh            Summary: 5 passed, 15 failed
bash scripts/verify-dependency-direction.sh       Summary: 6 passed, 0 failed
bash scripts/verify-v1-architecture.sh            Summary: 12 passed, 0 failed
bash scripts/verify-package-layout.sh             package-layout verification passed
bash scripts/verify-starmap-ownership.sh          Summary: 12 passed, 0 failed
bash scripts/verify-openrouter-parity.sh          Summary: 16 passed, 0 failed
bash scripts/verify-model-modalities.sh           Summary: 26 passed, 0 failed
bash scripts/verify-files-api.sh                  Summary: 22 passed, 0 failed
bash scripts/verify-async-media-jobs.sh           Summary: 18 passed, 0 failed
bash scripts/verify-catalog-performance.sh        Summary: 20 passed, 0 failed
bash scripts/verify-auth-onboarding.sh            Summary: 26 passed, 0 failed
bash scripts/verify-console-session-grants.sh     Summary: 16 passed, 0 failed
bash scripts/verify-v1-release.sh                 Summary: 16 passed, 0 failed
bash scripts/verify-developer-experience.sh       Summary: 47 passed, 0 failed
bash scripts/verify-console-modernization.sh      Summary: 21 passed, 0 failed
bash scripts/verify-catalog-driven-providers.sh   Summary: 19 passed, 0 failed
bash scripts/verify-doc-links.sh                  PASS
bash scripts/verify-readme-quickstart.sh          PASS
bash scripts/verify-release-workflow.sh           PASS
bash scripts/verify-action-pins.sh                16 references match
bash scripts/test-dependency-direction-verifier.sh PASS
bash scripts/test-doc-link-verifier.sh            PASS
go build ./... && go vet ./... && go test ./...   ok
make lint                                         0 issues
make build                                        ok
```

The five conditions that pass are `PLG-V01` through `PLG-V05`. The count is
monotone with the plan phases, so the gate now reports what the tree really
holds.
