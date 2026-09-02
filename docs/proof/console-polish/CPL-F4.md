# CPL-F4 proof: chat

Branch `codex/cpl-f4`. Base: the CPL-F3 squash `75726b7`.

## What changed

| Area | Change |
| --- | --- |
| `lib/modelFilter.ts` | `chattableModels` now keeps a model only when an offering serves `chat-completions`. An offering that names no operation, and a model with no offerings, still stay. |
| `lib/modelFilter.ts` | New `defaultChatModel(remembered, models, usableProviders)` owns the default model rule. |
| `routes/chat.tsx` | The default effect calls `defaultChatModel` on every catalog or status change. A remembered model that no longer routes chat is replaced. |
| `routes/chat.tsx` | The greeting reads `Try a model through this gateway`. The suggestion label reads `Starter prompts` in sentence case. |
| `routes/chat.tsx`, `chat/ModelPicker.tsx`, `chat/ThreadList.tsx` | The three uppercase section labels drop `uppercase tracking-wide` for `font-medium`. |
| Every chat file | 34 decorative icons carry `aria-hidden`. The four capability icons in the picker carry `role="img"` beside their labels. |
| `chat/Messages.tsx` | Each assistant turn shows the maker mark beside the model name through `EntityLogo`. |
| `routes/chat.test.tsx` | New. Four tests cover the default rule and the picker filter. |
| `lib/modelFilter.test.ts` | Five tests cover the chat filter and the default rule. |
| `chat/Messages.test.tsx` | One test covers the maker mark. |

## Design notes

**The picker filter is positive.** The live catalog on the dev gateway lists 511 models, and 415 of them serve chat completions. The old rule hid only rerank and document recognition. So the picker kept 80 models that answer no chat turn. Embeddings, speech, transcription, moderation, image generation, and video generation each reach the gateway through another route. The new rule keeps a model when any offering names `chat-completions`. A model such as `alibaba/wan2.6-t2i` serves chat on one provider and image generation on another, and it stays.

**The default rule lives in one function.** The route effect held the fallback and skipped it whenever the composer already held a model. A remembered model that the catalog no longer routes as chat kept the composer on a model that cannot answer. The new `defaultChatModel` keeps a remembered chat model or a preset. Otherwise it picks the first chat model that a provider with a usable operator credential serves. The catalog carries no rank field, so catalog order is the rank.

**The mark names the maker.** The gateway response names no routed provider, so the console cannot name the provider that served a turn. The mark beside the model name is the catalog author mark, which is the maker of the model. The gateway gap is out of scope here, with the proxy as owner.

**Icons.** A decorative icon inside a labeled control is hidden from the accessibility tree. A capability icon in the picker already carried an `aria-label`, and an SVG needs `role="img"` for that label to reach a reader.

## Counts

| Measure | Before | After |
| --- | --- | --- |
| Vitest tests | 389 | 399 |
| Entry chunk gzip | 119.21 kB | 119.17 kB |
| Verifier | 42 passed, 6 failed | 43 passed, 5 failed |

V43 is green. V44 through V48 stay red for CPL-F5 and CPL-Z1.

## Fail-before

At `9d5fd63`, the CPL-F3 head with the same tree as the squash, the new tests fail as follows.

| File | Result |
| --- | --- |
| `routes/chat.test.tsx` | 3 of 4 fail. The remembered-model test passes at baseline by design. |
| `lib/modelFilter.test.ts` | 4 of 5 new tests fail. |
| `chat/Messages.test.tsx` | 1 of 1 new test fails. |

Total: 8 failed, 18 passed across the three files.

## Tests added

| Test | Proves |
| --- | --- |
| a new conversation opens on the first chat model a credentialed provider serves | The default skips an embedding model in first place and an uncredentialed chat model. |
| the model the reader chose last wins while it still routes chat | A remembered chat model stays and shows as selected in the picker. |
| a remembered model that answers no chat turn is replaced | A stale remembered model yields to the rule. |
| the picker lists the models that answer a chat turn and no other | Embedding and rerank models leave the picker. |
| chattable models are the ones an offering serves through chat | The positive rule. |
| an undescribed model stays chattable | The unknown case stays visible. |
| an assistant turn carries the maker mark beside the model name | The mark falls back to initials without a bundled SVG. |

## Commands

| Command | Result |
| --- | --- |
| `pnpm typecheck` | clean |
| `vitest run` | 63 files, 399 tests pass |
| `pnpm build` | entry 119.17 kB gzip |
| `scripts/verify-console-polish.sh` | 43 passed, 5 failed |
| 23 `scripts/verify-*.sh` gates | see the pull request |

## Visual check

The dev console on `127.0.0.1:5174` shows the new greeting and the `Starter prompts` label. The picker section labels read in sentence case. The video-only `alibaba/wan2.6-t2v` model is absent from the picker.

UNVERIFIED live: the maker mark on a real assistant turn. The dev gateway refuses every chat request with `Provider credentials are not configured`, so no live turn carries a model. The jsdom test covers the mark and its initials fallback.
