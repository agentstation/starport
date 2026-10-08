# Record with scripts/record-cli-demo.py. It supplies isolated local state.
Output cli.svg
Output cli.gif
Output cli.mp4

Set Shell "bash"
Set Width 1280
Set Height 720
Set FontFamily "Geist Mono"
Set FontSize 16
Set Theme "Dracula"
Set Padding 20
Set WindowBar Colorful
Set WindowBarTitle "Starport · CLI"
Set Framerate 20
Set TypingSpeed 35ms

Hide
Set TypingSpeed 0ms
Type `source setup.sh`
Enter
Wait+Line@10s /^[$]$/
Type `printf '\033[2J\033[H\033[1;36mGoal / Choose a chat model and check a local gateway\033[0m\nFor a local app: inspect a model, start Starport, and verify catalog access.\n\n\033[1;36m1 / Select a chat model\033[0m\nFind the exact model ID and inspect its chat offering.\n\n'`
Enter
Wait+Line@10s /^[$]$/
Show
Set TypingSpeed 35ms
Sleep 3s

Type `starport models search gpt-6.1-sol`
Enter
Wait+Line@10s /^[$]$/
Sleep 2s
Type `starport models show openai/gpt-6.1-sol`
Enter
Wait+Line@10s /^[$]$/
Sleep 3s

Hide
Set TypingSpeed 0ms
Type `printf '\033[2J\033[H\033[1;36m2 / Start an authenticated gateway\033[0m\nStart a local session and confirm that its HTTP API is ready.\n\n'`
Enter
Wait+Line@10s /^[$]$/
Show
Set TypingSpeed 35ms
Sleep 2s

Type `starport dev --no-open >gateway.log 2>&1 &`
Enter
Wait+Line@10s /^[$]$/
Sleep 1s
Hide
Set TypingSpeed 0ms
Type `gateway_pid=$!; printf '%s\n' "$gateway_pid" >gateway.pid`
Enter
Wait+Line@10s /^[$]$/
Type `demo-control ready`
Enter
Wait+Line@60s /^[$]$/
Type `export STARPORT_API_KEY=$(cat gateway.key)`
Enter
Wait+Line@10s /^[$]$/
# Clear hidden setup commands before resuming the real banner and API checks.
Type `printf '\033[2J\033[H\033[1;36m2 / Start an authenticated gateway\033[0m\nThe session is running. Inspect its URL and HTTP readiness.\n\n'`
Enter
Wait+Line@10s /^[$]$/
Show
Set TypingSpeed 35ms
Type `cat gateway-banner.txt`
Enter
Wait+Line@10s /^[$]$/
Sleep 2s
Type `url=http://127.0.0.1:19335`
Enter
Wait+Line@10s /^[$]$/
Type `curl -fsS --max-time 5 "$url/health/ready" | tee health.json | jq .`
Enter
Wait+Line@10s /^[$]$/
Sleep 2s

Hide
Set TypingSpeed 0ms
Type `printf '\033[2J\033[H\033[1;36m3 / Check the selected model\033[0m\nUse the gateway API key to check the selected provider offering.\n\n'`
Enter
Wait+Line@10s /^[$]$/
Show
Set TypingSpeed 35ms
Sleep 2s

Type `curl -fsS --max-time 5 -H "Authorization: Bearer $STARPORT_API_KEY" "$url/api/v1/catalog/discovery" >discovery.json`
Enter
Wait+Line@10s /^[$]$/
Type `jq '.models[] | select(.id=="openai/gpt-6.1-sol") | {id, offerings}' discovery.json`
Enter
Wait+Line@10s /^[$]$/
Sleep 4s

Hide
Set TypingSpeed 0ms
Type `demo-control closing`
Enter
Wait+Line@10s /^[$]$/
Type `printf '\033[2J\033[H\033[1;36mResult / Model chosen and gateway checked\033[0m\n\nSelected: openai/gpt-6.1-sol\nVerified: gateway ready; authenticated catalog returned this model.\n\nNext: stop this development session, then set the provider credential.\nexport OPENAI_API_KEY="your-openai-provider-credential"\nRestart: starport dev --no-open\nClient: use <printed gateway URL>/v1 and <new gateway API key>.\n\nProvider credential: inference access. Gateway key: client authentication.\nThis demo sent no inference request.\n'`
Enter
Wait+Line@10s /^[$]$/
Show
Sleep 8s

Hide
Type `kill -INT "$gateway_pid"; wait "$gateway_pid"; printf '0\n' >shutdown.txt`
Enter
Wait+Line@10s /^[$]$/
Type `demo-control verify; touch complete`
Enter
Wait+Line@10s /^[$]$/
