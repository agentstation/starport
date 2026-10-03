---
title: Errors and limits
area: api-compatibility
order: 3
summary: Read the error body of each API family, and learn the status codes for authentication, scopes, rate limits, budgets, request size, and guardrails.
---

Starport writes each error in the format of the API family that the client called. A client of the OpenAI family gets an OpenAI error object. A client of the OpenRouter family gets an OpenRouter error object.

## Error bodies

OpenAI family, under `/v1`:

```json
{
  "error": {
    "message": "Insufficient API key scope",
    "type": "permission_error"
  }
}
```

The object can also contain `param` and `code`.

OpenRouter family, under `/api/v1`:

```json
{
  "error": {
    "code": 403,
    "message": "Insufficient API key scope",
    "metadata": { "error_type": "permission_error" }
  }
}
```

The OpenRouter `code` is the HTTP status. The `error_type` value is the same as the OpenAI `type` value.

## Status codes

| Status | Type | Message or cause |
| --- | --- | --- |
| `400` | `invalid_request_error` | The request is not valid. A guardrail refusal also uses `400`. |
| `401` | `authentication_error` | `Not authenticated` |
| `402` | `permission_error` | `Insufficient quota: <scope> <dimension> budget exhausted for the current <interval> window` |
| `403` | `permission_error` | `Insufficient API key scope` or `Admin access required` |
| `404` | `not_found_error` | `The requested endpoint does not exist`, or a resource does not exist |
| `405` | `invalid_request_error` | `Method not allowed` |
| `413` | `invalid_request_error` | `Request body is <n> bytes, above the <limit> byte limit` |
| `429` | `rate_limit_error` | `Rate limit exceeded: <scope> request limit` |
| `503` | `server_error` | For example, `The catalog is not available.` |

A `402` error is a budget refusal, not a provider payment error. The `<scope>` value is `account`, `key`, or `team`.

## Rate limits

Starport counts requests for each account, key, and team that has a request limit. The tightest limit sets the response headers:

| Header | Meaning |
| --- | --- |
| `X-RateLimit-Limit` | The request limit of the window |
| `X-RateLimit-Remaining` | The requests that remain in the window |
| `X-RateLimit-Reset` | The end of the window |
| `X-RateLimit-Scope` | `account`, `key`, or `team` |
| `Retry-After` | Seconds to wait. A `429` response adds it. |

| Setting | Default |
| --- | --- |
| `STARPORT_SECURITY_ENABLE_RATE_LIMITING` | `true` |
| `STARPORT_RATE_LIMITING_DEFAULT_REQUESTS_PER_MINUTE` | `60` |
| `STARPORT_RATE_LIMITING_WINDOW_SIZE` | `1m` |

A `429` response tells the client to wait. Send the request again after the `Retry-After` value.

## Budgets

A budget limits spend or tokens for an account, a key, or a team in an interval. Starport reserves budget before each provider attempt. `STARPORT_BUDGET_ADMISSION_MODE=atomic` is the only supported mode. Only routes that start paid work check a budget. A read of a job or a file does not.

These headers report the state of each budget:

| Header | Meaning |
| --- | --- |
| `X-Starport-Budget-Spend-Limit`, `-Remaining`, `-Reset`, `-Scope` | The spend budget, in nano-USD |
| `X-Starport-Budget-Tokens-Limit`, `-Remaining`, `-Reset`, `-Scope` | The token budget |

A cache hit does not skip a required budget check. Refer to [Keys and roles](../start/keys-and-roles.md) for the scopes and limits of a key.

## Request limits

| Setting | Default | Effect |
| --- | --- | --- |
| `STARPORT_SERVER_MAX_REQUEST_SIZE` | `33554432` | The largest request body in bytes. A larger body gets `413`. |
| `STARPORT_SERVER_REQUEST_TIMEOUT` | `60s` | The time limit of one request |
| `STARPORT_FILES_MAX_UPLOAD_BYTES` | `536870912` | The largest file upload in bytes |

## Guardrails

Guardrails are off until `STARPORT_GUARDRAILS_CHECKS` names a check. The `pii` check finds email addresses, phone numbers, card numbers, and US social security numbers. Its mode is `redact` (the default) or `refuse`. The `moderation` check sends the text to a moderation model and refuses a score at or above the threshold.

```dotenv
STARPORT_GUARDRAILS_CHECKS=pii,moderation
STARPORT_GUARDRAILS_PII_MODE=redact
STARPORT_GUARDRAILS_MODERATION_MODEL=<provider>/<moderation-model>
```

A refusal returns `400` with the check name. A check that cannot run refuses the text. The moderation model must be a callable offering, and its call has its own usage record. Refer to the [operator guide](../../OPERATOR-GUIDE.md#guardrails).

## Limitations

- Neither family has a `/messages` route.
- The OpenRouter family has no image edit, audio translation, moderation, file, or batch route.
- The model lists show only routable offerings. A catalog model without a price, a ready adapter, or support is not in the list.
- A listed model can still fail at the provider. The model list does not test credentials.
- Discovery readiness is always `unknown` in this release.

Refer to [Supported API surfaces](surfaces.md) for each route.
