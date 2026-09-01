---
name: starport
description: Run and operate a self-hosted Starport LLM inference gateway. Use when a task must install Starport, start a gateway, answer model catalog questions, point a harness or SDK at the gateway, or diagnose a deployment.
license: AGPL-3.0-only
compatibility: Requires the starport CLI on PATH. The dev gateway binds 127.0.0.1:8080.
metadata:
  author: agentstation
---

# Starport

Starport is a self-hosted LLM inference gateway. It serves OpenAI-compatible
routes at `/v1` and OpenRouter-compatible routes at `/api/v1`. One gateway
routes requests across every provider in its catalog and picks routes by
price, latency, or an explicit order.

Keep the two credential kinds separate. A gateway API key authenticates a
client to Starport. A provider credential pays a provider. Never use one
where the other belongs.

## Install

Install the released cask on macOS or Linux:

```bash
brew install agentstation/tap/starport
starport --version
```

GitHub Releases at `agentstation/starport` carries checksummed archives for
macOS, Linux, and Windows.

## Start a development gateway

Export one provider credential, then start an isolated gateway:

```bash
export OPENAI_API_KEY="replace-with-provider-inference-key"
starport dev --no-open
```

The command starts a loopback gateway on port 8080 with in-memory state.
It prints one temporary gateway API key and a
one-time console launch link. Add `--no-open` to print the link instead of
opening a browser, which fits an agent session. The gateway blocks the terminal,
so run it in a background process and keep it running.

Wait for readiness before the first request:

```bash
curl --fail http://127.0.0.1:8080/health/ready
```

## Start a durable gateway

For state that survives restarts, initialize once and then serve:

```bash
starport init
starport serve
```

`starport init` writes local configuration, creates encrypted storage, and
prints the first gateway API key once. Store that key: no later command
prints it again. When a browser must reach the console without a launch
link, it presents this machine's local admin token instead.
`starport auth token --copy` puts that token on the clipboard.

## Point a client at the gateway

Use the printed gateway API key as the bearer token.

- OpenAI SDK or harness: base URL `http://127.0.0.1:8080/v1`.
- OpenRouter SDK or harness: base URL `http://127.0.0.1:8080/api/v1`.

```bash
export STARPORT_API_KEY="replace-with-gateway-api-key"
curl --fail-with-body http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"ping"}]}'
```

Model IDs use the `provider/model` form. A bare model name is not routable.

## Answer catalog questions

The CLI answers catalog questions offline from the embedded catalog. Use
`--json` for machine-readable output.

```bash
starport models search gpt-4o --json
starport models show openai/gpt-4o-mini --json
```

`models search` matches every query word against model IDs, names, and
authors. It answers a compact summary: ID, name, context length, and token
prices. `models show` answers the full projection for one exact model ID.
That projection carries prices, capabilities, modalities, and every routable
provider offering.

## Diagnose a deployment

```bash
starport doctor --json
starport config validate
starport config show --json
```

`starport doctor` runs read-only checks on paths, configuration, the master
key, the catalog, and provider adapters. Add `--probe` to inspect configured
storage and API keys in read-only mode.

## Read more

- Run `starport agent setup` after a CLI upgrade to reinstall this skill.
- `starport --help` lists every command.
- The repository README at `agentstation/starport` documents the quick start.
- `docs/OPERATOR-GUIDE.md` in that repository documents operations:
  budgets, presets, caching, guardrails, webhooks, and telemetry export.
