# RNK3 Canonical rerank types

The file `internal/inference/rerank.go` holds the rerank request, the result,
and the response. Phase B starts here, because every layer below the codec
reads this shape rather than a provider's.

## The shape decision

Four providers serve reranking and no two agree on the wire. One names the
result count `top_n` and another names it `top_k`. One returns the ranked list
under `results` and another under `data`. One requires the document text echoed
on every result and another has no field for it.

The canonical result carries an index and a relevance score. It carries no
document text. Two of the four wire shapes echo the text, so the tempting
canonical form holds a copy, and a copy costs twice.

A thousand-document batch is the request size Cohere and Voyage both accept. A
response that copied every document would hold that batch twice in memory for
the length of one turn. The copy would also let the response disagree with the
request that produced it, and nothing downstream could tell which one was
right.

The method `RerankResponse.Documents` resolves a response back against its
request. A codec that has to echo the text calls it. A result that names a position the request never held returns `ErrRerankResultOutOfRange`. An empty string would read as a document that happened to be blank.

## What construction refuses

`NewRerankRequest` refuses an empty query and an empty document list. A provider bills for both and can answer neither. An empty query scores every document against nothing. An empty list ranks nothing.

The two other request fields carry no such rule. The field `TopN` asks for fewer results, and nil asks for all of them. `MaxTokensPerDocument` caps how much of each document the provider reads. Nil leaves the provider default in place. The cap is a cost control as well as a size control. A provider that exceeds it splits the document into chunks it bills separately.

## The import guard

`TestCanonicalPackageNamesNoProtocol` reads this package's own import list
through `go/build` and fails on any import under `internal/protocol` or
`internal/providers`. The canonical shape exists so that no layer below the
codec carries a provider name, and an import is the fastest way to lose that.
The guard covers the package rather than one file, because the rule is the
package's rule.

## Evidence

| Command | Result |
| --- | --- |
| `go test ./internal/inference/...` | ok, 4 new tests |
| `go test ./...` | exit 0 |
| `go build ./...` | clean |
| `make lint` | 0 issues |
| `bash scripts/verify-dependency-direction.sh` | 6 passed, 0 failed |
| `bash scripts/verify-reranking.sh` | 1 passed, 21 failed |

`RNK-V05` is the condition that turned green. It is the first of the twenty-two
to pass, because phase A changed Starmap alone and the gate reads this tree.

## Fail-before

At the baseline commit `dc3788d` the package holds no rerank type. The new file
`internal/inference/rerank_test.go` fails to compile against that tree, and the
verifier reported 0 passed, 22 failed.
