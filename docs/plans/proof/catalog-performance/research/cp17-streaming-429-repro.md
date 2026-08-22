# CP17 live repro: streaming 429 becomes an empty 200

Recorded 2026-08-21 during CM12 capture attempts against the local gateway
(`:8080`, groq credential, model `groq/compound-mini`). This note preserves
the evidence for CP17 ("Failure normalization — streaming 429 not an empty
200; never cache empty completions") before the fix.

## Observed defect

When groq rejects a request over its daily token quota *inside* an
accepted stream, it returns HTTP 200 and emits the failure as an SSE
`event: error` frame. The gateway stream codec drops that frame. The
client then receives:

- one chunk with `"choices":[]` and a usage object,
- `data: [DONE]`.

The console renders "The model returned no content." with `↑0` tokens.
The request log records a success. No failure entry exists. The empty
completion is eligible for the response cache.

## Evidence chain

1. Non-streaming control. `POST /v1/chat/completions` (stream false) with
   the same model over quota returned a proper 429 through the gateway.
   The failure path works for non-streaming responses.
2. Gateway stream. The same request with `stream: true` returned HTTP 200,
   one empty chunk (`"choices":[]`, usage only), then `[DONE]`. Tried at
   `max_tokens` 600 and 2048 — identical result, so it is not a token cap.
3. Provider direct. `curl -N https://api.groq.com/openai/v1/chat/completions`
   with `stream: true` and the same credential returned HTTP 200 with:

   ```
   event: error
   data: {"error":{"message":"Rate limit reached ... on tokens per day (TPD) ...","type":"tokens","code":"rate_limit_exceeded"}}
   ```

   The 429-shaped JSON body arrives inside the 200 stream as an
   `event: error` SSE frame — the frame the gateway swallows.

## Quota mechanics (capture planning)

- Compound-system requests carry a fixed ~3,289–3,299-token admission
  estimate regardless of `max_tokens`, so a two-column compare needs
  roughly 6,600 tokens of daily headroom.
- Headroom decays back at ~69 tokens/minute (derived from groq's own
  retry arithmetic: 2,327 tokens over 33.5 minutes).

## Required behavior (CP17 acceptance)

- Map a provider SSE `event: error` frame to a canonical stream failure:
  surface the provider status (429 here) and message to the client, and
  record it in the request log as a failure.
- Never cache a completion whose choices are empty.
- Add a stream-codec contract test that feeds an `event: error` frame and
  asserts the canonical failure event, plus a cache test that refuses an
  empty completion.
