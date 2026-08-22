Competitor and UX research report for the Starport console. All findings verified by live fetch or search, Aug 2026.

## 1. OpenRouter catalog UX (openrouter.ai)

**Model page** (e.g. openrouter.ai/openai/gpt-4o) is the richest page and the pattern to copy. Header: name, input/output price as "$2.50 / $10 per 1M", context ("128K"), created date, knowledge cutoff. Tab/section order: Providers, Pricing, Performance, Uptime, Benchmarks, Apps, Activity, FAQ. The per-provider table is the core artifact — columns: Provider, "Input /M", "Output /M", "Cache read /M", "Latency", "Throughput" (tok/s), "Uptime" (%). Also "Uptime (3d)" / "Availability (3d)" percentages with a 72-hour availability graph, and an Apps section ranking consumer apps by token volume (social proof). "More models from OpenAI" cross-links to the author page.

**Models listing** (openrouter.ai/models): search (⌘K), list/table view toggle, Filter button. Each row: author-prefixed name ("Meta: ..."), context ("1.05M context"), "$0.10/M input tokens $0.20/M output tokens", total token volume ("292B tokens"), category rank badges ("Programming (#24)"), description, release date, variant tags ("Free variant", "Batch variant", deprecation notices like "Going away September 30, 2026").

**Author/lab page** (openrouter.ai/anthropic): headline "Access N {Author} models through the OpenRouter unified API", aggregate "tokens processed" stat, then a chronological model catalog (newest first) with per-model token volume, context, pricing, variant tags. Pure catalog — no charts.

**Provider page** (openrouter.ai/provider/groq): thinnest of the three — name, ToS link, model count, model cards with pricing/context/description. Notably NO provider-level uptime/latency/throughput aggregates. That is a gap Starport could beat: put per-provider health charts on the provider page.

**Provider routing presentation** (openrouter.ai/docs/features/provider-routing): default is price-weighted load balancing (inverse-square of price, deprioritize providers with recent outages), explicit `sort` overrides: "price" / "throughput" / "latency" (sort disables load balancing — providers tried in order). Shortcuts `:nitro` (throughput + priority tier) and `:floor` (price + flex tier) as model-slug suffixes. Perf stats are rolling 5-minute windows at p50/p75/p90/p99 for latency (seconds) and throughput (tokens/second).

## 2. Gateway overhead claims

| Product | Claim | What it measures | Per-request surfacing |
|---|---|---|---|
| LiteLLM | "adds < 50ms in most setups"; Rust gateway "~0.7ms p99"; Python v1 measured 257.7ms in their own Rust benchmark | proxy overhead vs mock upstream (network_mock mode) | YES — `x-litellm-overhead-duration-ms` on every response = "total response time minus the LLM API call time"; plus `x-litellm-timing-pre-processing-ms` / `-llm-api-ms` / `-post-processing-ms` behind `LITELLM_DETAILED_TIMING=true` (docs.litellm.ai/docs/troubleshoot/latency_overhead) |
| Portkey | "sub-1ms latency added by the gateway"; real-world community reports 20-40ms with guardrails/routing | proxy forwarding only | Prometheus metrics for self-host; no standard per-request header |
| Helicone | "<1ms" gateway overhead; "sub-5ms P95" | mock upstream with 60ms median simulated provider latency; README admits "real world usage will see much higher latency" (github.com/Helicone/ai-gateway benchmarks/README.md) | no |
| Vercel AI Gateway | "under 20 ms", ~10ms cited for gateway routing | control-plane routing, not end-to-end; community reports up to 300-700ms extra on some Vertex routes | dashboard observability, not headers |
| Cloudflare AI Gateway | no official ms number; community 10-50ms | edge proxy | dashboard logs |
| Kong AI Gateway | ~3-5ms (community); full Kong plugin pipeline runs before AI logic | proxy overhead | no |
| OpenRouter | deliberately no number — "designed to add minimal latency", "runs at the edge" via Cloudflare Workers | — | no |

Takeaways: (a) every vendor benchmarks against a mock upstream and states proxy-only overhead — the honest framing is "gateway overhead", never TTFT; (b) LiteLLM is the ONLY one exposing per-request overhead in a response header, and it's a beloved trust feature — a `x-starport-overhead-duration-ms` equivalent (plus a console column) would be a differentiator; (c) safe claim phrasing pattern: "adds <Xms p99 gateway overhead (excludes provider inference time)" with methodology published, since third parties (deepinspect.ai/blog/ai-gateway-latency-benchmarks) now call out mock-upstream inflation.

## 3. Composer "+" vs model selector

**ChatGPT** (aiuxplayground.com/teardowns/chatgpt/composer/): one "+" menu groups three categories — attachments ("Attach"), creation tools ("Create image"), and behavioral modes ("Thinking", "Deep research", "Web search"), with "More" and "Projects" as overflow. Selecting a mode adds a removable chip in the composer bar and swaps the placeholder text (e.g. "Get a detailed report" for Deep research). Their stated principle: "You pick intent, not infrastructure." Labels are outcome verbs, not model names.

**Claude web/desktop**: "+" holds attachments and tools (web search, code interpreter, connectors/MCP); active tools show as chips above the input. Model selector is a separate dropdown at the top of the conversation; "Extended thinking" toggle lives UNDER the model selector (it's a model behavior, not a tool). Research is a separate toggle at the bottom-left of the composer.

**t3.chat**: model selector is the hero control (dropdown with keybindings, favorites/pinning is the top community request); search grounding is a per-message toggle; attachments a simple paperclip. Minimal, keyboard-first.

**Convention that emerges**: attachments + capability toggles (web search, deep research, tools/connectors) live behind "+" as chips; model choice + model-level behavior (thinking effort, presets) live in the model selector. Deep research is universally a mode toggle in the composer, never a model in the picker.

## 4. Design language for enterprise-trust devtools

- **Vercel (Geist)**: Title Case for dashboard buttons/controls ("Create Project"), sentence case for marketing headlines; ellipsis character for in-progress labels ("Saving…"); errors state what failed + what to do next. (vercel.com/geist/introduction)
- **Stripe**: sentence case throughout dashboard/docs — nav item is "API keys", not "API Keys" (docs.stripe.com/keys, dashboard "Developers → API keys").
- **Linear**: sentence case, terse nouns; settings nav uses bare "API", "Members", "Security".
- **insforge (docs.insforge.dev/core-concepts/ai/overview)**: Title Case proper nouns, sentence case descriptions; providers appear as inline capitalized names + "provider chips" in dashboard screenshots; frames the gateway as "one OpenAI-compatible endpoint".
- Net: modern devtool consensus is sentence case for nav/labels ("API keys"), wordmark as set by the brand (Vercel/Linear lowercase-ish logotypes, Stripe title case). If Starport wants the enterprise-trust look, sentence-case everything except proper nouns.
- **Logos — lobehub/lobe-icons**: MIT-licensed, hundreds of AI provider/model icons, shipped as React components (@lobehub/icons), React Native, and dependency-free static packages: @lobehub/icons-static-svg, -static-png (light/dark variants), -static-webp; mono + color variants; tree-shakable. For an air-gapped bundle, vendor @lobehub/icons-static-svg at build time (no CDN). Caveat: the MIT license covers the icon code/assets; the trademarks themselves belong to their owners — nominative use to identify providers in a catalog is the same posture OpenRouter/LiteLLM take, but don't imply endorsement.

## 5. TTFT/latency labeling conventions

- **Artificial Analysis** (artificialanalysis.ai/methodology/performance-benchmarking) — the reference vocabulary: "Time to First Token" (seconds; request sent → first token), "Time to First Answer Token" (excludes reasoning tokens), "Output Speed" ("output tokens per second, after the first token is received"), "End-to-End Response Time". Reported as median (P50) over 72h.
- **OpenRouter**: label is "Latency" in tables but explicitly defined as time to first token ("latency (time to first token)", seconds); "Throughput" = output tokens/sec, unit rendered "tok/s". For reasoning models, latency = time to first reasoning token; throughput includes reasoning tokens. Uptime as "Uptime (3d)" percentage.
- **Groq**: console shows server-side metrics; speed rendered as "tokens/s" or "T/s"; TTFT quoted in seconds ("0.18s TTFT P50").
- Recommended Starport labels: "Latency (TTFT)" or "Time to first token" in seconds with 2 decimals; "Throughput" in "tok/s"; percentile suffix as lowercase "p50/p99" (OpenRouter style) or "P50" (AA style) — pick one and stick to it; "Uptime (3d)" for availability windows.

## Synthesis — what Starport should adopt

1. Model page = the flagship catalog surface: header (price/context/created/cutoff) + per-provider comparison table (Input /M, Output /M, Cache read /M, Latency, Throughput, Uptime) + 72h availability chart. Provider and author pages cross-link through it; beat OpenRouter by putting health charts on provider pages too.
2. Surface per-request gateway overhead in a response header (LiteLLM's `x-litellm-overhead-duration-ms` pattern) and in the console request log; claim overhead as "p99 gateway overhead, excluding provider inference" with published methodology.
3. Composer: "+" = attachments + capability chips (web search, deep research); model selector = model + reasoning/preset controls. Chips above/in the input show active modes; placeholder text changes per mode.
4. Copy: sentence case ("API keys"), Artificial Analysis metric vocabulary, "tok/s" units.
5. Icons: vendor @lobehub/icons-static-svg (MIT) into the bundle for air-gapped use; mono variants for nav, color for catalog cards.

Key URLs: openrouter.ai/openai/gpt-4o, openrouter.ai/docs/features/provider-routing, docs.litellm.ai/docs/troubleshoot/latency_overhead, docs.litellm.ai/docs/benchmarks, github.com/Helicone/ai-gateway (benchmarks/README.md), vercel.com/docs/ai-gateway, aiuxplayground.com/teardowns/chatgpt/composer/, artificialanalysis.ai/methodology/performance-benchmarking, github.com/lobehub/lobe-icons, vercel.com/geist/introduction, docs.insforge.dev/core-concepts/ai/overview, deepinspect.ai/blog/ai-gateway-latency-benchmarks.