# Starport CLI capture

The VHS tape types real shell commands at 35 milliseconds per character.
The commands inspect the embedded catalog and a temporary authenticated gateway.
The capture uses an isolated home and permits only loopback network access.
The capture redacts the gateway API key and console launch link. It makes no provider inference request.

## Story and pauses

Cyan editorial headings state the next action and remain above the actual shell commands.
The opening pairs the local app goal with model selection. The result screen follows verified readiness and authenticated catalog access.

The gateway remains live through the result screen. Hidden cleanup follows it.

The next action stops the development session, sets an OpenAI provider credential, and starts a new session. Use that session’s gateway key with its printed URL plus `/v1`.
The provider credential authorizes inference. The gateway key authenticates the client. This capture does not verify inference.

| Segment | Hold | Reason |
| --- | --- | --- |
| goal and first chapter | 3 seconds | Read the goal and model selection step together. |
| later chapter headings | 2 seconds | Read the next action before command entry. |
| model search | 2 seconds | Compare the model matches. |
| model detail | 3 seconds | Read context, price, and provider operation. |
| gateway launch | 1 seconds | Read the actual development command before its isolated startup checks. |
| gateway banner | 2 seconds | Read the URL and authentication mode. |
| readiness | 2 seconds | Confirm the actual gateway status. |
| catalog offerings | 4 seconds | Read the canonical model, exact provider ID, operations, and unknown readiness. |
| result and next action | 8 seconds | Read the verified result, restart commands, and credential roles. |

Prompt and readiness checks wait for actual completion. Credential capture and shutdown remain hidden.

## Catalog and build evidence

This source build uses a reviewed local Starmap catalog. It is not a new public release.
The reviewed catalog includes uncommitted local changes.
The build joins the Starport and Starmap source trees with a temporary Go workspace. It leaves their module files unchanged.

The Starmap source commit is `c3e38970db9c23d3023972b71bce132ee6efbb39`.
The embedded generation is `bindings-a09b2fbbb3a32b1f5cbe5d745746e66bf46c520175934205b7bcef3498690915.local.72ae50b97f55`.
Its catalog payload checksum is `sha256:ad6b71e899dd9b5e0f4ee4195cdc4b07117447479e7f6b11117245ef33a68eb9`.

The manifest SHA-256 is `f1051d8babd1f4c5f84747b2933f2e8cd2f50fb6bd40c2b7a40d9e347171253a`.
The input inventory SHA-256 is `d6a6b26476a78af06f0c50bfe9f6ac2af44ec9c18fe746bad17c1669a3d8ef75`.
The record lists each catalog and source input hash. The recorder verifies the manifest and both selected model files inside the binary.
It also rejects catalog input changes during capture.

The selected canonical ID is `openai/gpt-6.1-sol`. Its OpenAI provider ID is `gpt-6.1-sol`.
The demo checks the chat offering. It makes no inference request or tool call.

## Reproduce

Run the commands from the repository root.
The recorder needs macOS `sandbox-exec`, Go, Python 3, `ttyd`, `ffmpeg`, `ffprobe`, `jq`, and Google Chrome.
The capture font is `scripts/demo-font/GeistMono-Regular.woff2` under the SIL Open Font License.
Use the `agentstation/vhs` fork with elapsed timing and `--svg-font-file` support.
This capture uses local VHS fixes that no published upstream release contains.
Build that VHS source into `/tmp/agentstation-demo-vhs` before the recording command.

Use the reviewed Starmap source and catalog identified above. The published module does not supply this local catalog update.

```bash
starport_source=$PWD
starmap_source=/path/to/reviewed/starmap
demo_workspace=$(mktemp -d /tmp/starport-demo-workspace.XXXXXX)
GOWORK=off go -C "$demo_workspace" work init "$starport_source" "$starmap_source"
GOWORK="$demo_workspace/go.work" go build -o /tmp/starport-cli-demo ./cmd/starport
python3 scripts/record-cli-demo.py \
  --binary /tmp/starport-cli-demo \
  --starmap-source "$starmap_source" \
  --vhs /tmp/agentstation-demo-vhs \
  --browser-path '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' \
  --output "$(mktemp -d /tmp/starport-cli-demo.XXXXXX)"
```

## Command preflight

This transcript runs the exact tape commands before recording. The media repeats them with fresh state.

The transcript removes ANSI display controls. It preserves command text and product output.

```text
$ source setup.sh
$ printf '\033[2J\033[H\033[1;36mGoal / Choose a chat model and check a local gateway\033[0m\nFor a local app: inspect a model, start Starport, and verify catalog access.\n\n\033[1;36m1 / Select a chat model\033[0m\nFind the exact model ID and inspect its chat offering.\n\n'
Goal / Choose a chat model and check a local gateway
For a local app: inspect a model, start Starport, and verify catalog access.

1 / Select a chat model
Find the exact model ID and inspect its chat offering.

$ starport models search gpt-6.1-sol
openai/gpt-6.1-sol  GPT-6.1 Sol  context=1050000  prompt=2e-06 completion=1e-05
1 models match
$ starport models show openai/gpt-6.1-sol
openai/gpt-6.1-sol  GPT-6.1 Sol
context: 1050000
pricing per token: prompt=2e-06 completion=1e-05 USD
offering: openai/gpt-6.1-sol operations=chat-completions
$ printf '\033[2J\033[H\033[1;36m2 / Start an authenticated gateway\033[0m\nStart a local session and confirm that its HTTP API is ready.\n\n'
2 / Start an authenticated gateway
Start a local session and confirm that its HTTP API is ready.

$ starport dev --no-open >gateway.log 2>&1 &
$ gateway_pid=$!; printf '%s\n' "$gateway_pid" >gateway.pid
$ demo-control ready
$ export STARPORT_API_KEY=$(cat gateway.key)
$ printf '\033[2J\033[H\033[1;36m2 / Start an authenticated gateway\033[0m\nThe session is running. Inspect its URL and HTTP readiness.\n\n'
2 / Start an authenticated gateway
The session is running. Inspect its URL and HTTP readiness.

$ cat gateway-banner.txt
Starport development gateway
URL: http://127.0.0.1:19335
Authentication: required
Gateway API key (shown once): [value hidden]
Console (one-time launch link): [value hidden]
$ url=http://127.0.0.1:19335
$ curl -fsS --max-time 5 "$url/health/ready" | tee health.json | jq .
{
  "status": "ok",
  "timestamp": "2026-10-08T00:20:12Z",
  "service": "starport",
  "version": "dev"
}
$ printf '\033[2J\033[H\033[1;36m3 / Check the selected model\033[0m\nUse the gateway API key to check the selected provider offering.\n\n'
3 / Check the selected model
Use the gateway API key to check the selected provider offering.

$ curl -fsS --max-time 5 -H "Authorization: Bearer $STARPORT_API_KEY" "$url/api/v1/catalog/discovery" >discovery.json
$ jq '.models[] | select(.id=="openai/gpt-6.1-sol") | {id, offerings}' discovery.json
{
  "id": "openai/gpt-6.1-sol",
  "offerings": [
    {
      "provider": "openai",
      "provider_model_id": "gpt-6.1-sol",
      "availability": "unknown",
      "lifecycle": "unknown",
      "operations": [
        "chat-completions"
      ],
      "routable_operations": [
        "chat-completions"
      ],
      "readiness": "unknown"
    }
  ]
}
$ demo-control closing
$ printf '\033[2J\033[H\033[1;36mResult / Model chosen and gateway checked\033[0m\n\nSelected: openai/gpt-6.1-sol\nVerified: gateway ready; authenticated catalog returned this model.\n\nNext: stop this development session, then set the provider credential.\nexport OPENAI_API_KEY="your-openai-provider-credential"\nRestart: starport dev --no-open\nClient: use <printed gateway URL>/v1 and <new gateway API key>.\n\nProvider credential: inference access. Gateway key: client authentication.\nThis demo sent no inference request.\n'
Result / Model chosen and gateway checked

Selected: openai/gpt-6.1-sol
Verified: gateway ready; authenticated catalog returned this model.

Next: stop this development session, then set the provider credential.
export OPENAI_API_KEY="your-openai-provider-credential"
Restart: starport dev --no-open
Client: use <printed gateway URL>/v1 and <new gateway API key>.

Provider credential: inference access. Gateway key: client authentication.
This demo sent no inference request.
$ kill -INT "$gateway_pid"; wait "$gateway_pid"; printf '0\n' >shutdown.txt
$ demo-control verify; touch complete
```
