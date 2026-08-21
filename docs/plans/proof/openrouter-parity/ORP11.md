# ORP11 — Console key limits and budgets UI

Branch: `codex/orp-11-console-budgets` (from `codex/orp-10-key-budgets`)
Commit: 6aa4527. PR: #137 (base `codex/orp-10-key-budgets`).

## What shipped

1. Key create and edit modals share one prefillable form: allowed models
   (comma-separated IDs), expiry date, request limit with window
   (minute/hour/day, plus a custom option when an existing value differs),
   spend budget in USD with interval, and token budget with interval
   (day/week/month). Values validate client-side; spend converts to
   nano-USD; expiry submits as the inclusive end of day.
2. The key table gains a limits column: badges for allowed-model count,
   expiry, request limit, spend budget, and token budget, plus a lazy
   budget-usage line loaded per key from `GET /admin/keys/{id}`. A spent
   window renders a red `spend exhausted` or `tokens exhausted` badge;
   otherwise remaining allowance ("$X left", "N tok left").
3. Chat error surface names 402 and 429: "Budget exhausted — … Raise or
   clear this key's budget on the Keys page." and "Rate limited — …".
4. Usage status badges label 402 as `budget exhausted` and 429 as
   `rate limited`.
5. Guard test `TestKeyLimitsAndBudgetsShip` asserts the shipped strings in
   keys.js, api.js, and chat.js.

## Defects found and fixed by the walkthrough

- **Non-admin key creation always failed.** The console sent `scopes` only
  when the admin checkbox was checked, and the identity issuer requires at
  least one scope, so every non-admin create returned 400 "missing scopes".
  Latent since the console revamp. Fix: the create request always sends
  explicit scopes — `["admin"]` or the inference set `["chat:write",
  "embeddings:write", "models:read", "activity:read"]`.
- **Show-once secret rendered `[object Object]`.** The create response
  nests the record under `key` with the secret at `key.key`; the modal read
  `created.key` (the object). Fix: extract `key.key`, accepting a plain
  string for older shapes.

## Fail-before evidence

`git stash` of the six changed files → `TestKeyLimitsAndBudgetsShip` fails
(missing "allowed_models", "budgetExhausted", "Budget exhausted" strings);
restore → passes. The name-only create form (pre-ORP11 binary) was also
observed in the browser before the change.

## Acceptance walkthrough (browser, built gateway)

1. Created key `budget-demo` via the modal: spend $5/day, tokens 50/day.
   Secret displayed once and copied. Row shows scope badges, `$5.00 / day`,
   `50 tok / day`, `$5.00 left`, `50 tok left`.
2. First chat completion with the key (groq/compound-mini) returned 200 and
   consumed 613 tokens. Second request returned **402** with
   `X-Starport-Budget-Tokens-Limit: 50`, `-Remaining: 0`, `-Reset`, and the
   spend headers.
3. Keys page shows the red `tokens exhausted` badge on the row.
4. With the limited key active, the chat page shows "Budget exhausted —
   Insufficient quota: token budget exhausted for the current day window.
   Raise or clear this key's budget on the Keys page."
5. Usage view labels the 402 record `budget exhausted`.
6. Edit modal opens prefilled (spend 5/day, tokens 50/day); raising tokens
   to 1000 updates the row to `1.0k tok / day` with `387 tok left` and
   clears the exhausted badge.

## Verification

- Seven `scripts/verify-*.sh` gates: exit 0.
- `go test ./...`, `go vet ./...`, `make lint`, `make build`: exit 0.
- `scripts/smoke-openrouter-sdks.sh`: PASS (raw HTTP models/embeddings,
  Python, TypeScript, Go SDKs).
- Autoreview branch mode vs `origin/codex/orp-10-key-budgets`: clean, Sol
  high, confidence 0.99, zero findings.

## Recorded gaps

- The console Settings "Save & test" did not persist a programmatically set
  key value during automation (manual typing works); not reproduced as a
  user-facing defect, not chased in this task.
- `enforceBudgets` covers admin routes, so an exhausted admin key locks the
  console (design observation carried from ORP10).
