# MOD0 offering census

Source: the catalog that `github.com/agentstation/starmap v0.8.0` ships, read
through `starmap.NewContext` and `CurrentCatalogState`. Measured 2026-08-26.

## Model definitions, 591 total

| Modality | Input | Output |
| --- | --- | --- |
| text | 571 | 487 |
| image | 146 | 28 |
| pdf | 33 | 0 |
| video | 26 | 13 |
| audio | 23 | 19 |
| embedding | 0 | 34 |

## Provider offerings, 613 total

| Modality | Input | Output |
| --- | --- | --- |
| text | 611 | 522 |
| image | 170 | 29 |
| video | 57 | 13 |
| pdf | 50 | 0 |
| audio | 44 | 19 |
| embedding | 0 | 38 |

## Operations

| Operation | Offerings |
| --- | --- |
| chat-completions | 512 |
| embeddings | 38 |
| none | 63 |

Decision MOD-D1 cites the offering row. Input modalities outnumber output
modalities by roughly six to one, which is why phase A ships first.

MOD12 owns the 63 offerings that carry no operation. It states the residual
count and the reason for each residual after the media operations land.
