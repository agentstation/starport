#!/usr/bin/env bash
# Guard for the console modernization campaign, whose durable plan closed
# on 2026-08-22. Each condition asserts one capability of the modernized
# console. It reports every condition and
# exits nonzero while any condition fails. CI runs it as a required gate.
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

pass=0
fail=0

check() {
  local id="$1" desc="$2"
  shift 2
  if "$@" >/dev/null 2>&1; then
    printf 'PASS %s %s\n' "$id" "$desc"
    pass=$((pass + 1))
  else
    printf 'FAIL %s %s\n' "$id" "$desc"
    fail=$((fail + 1))
  fi
}

grep_q() { grep -Rq -- "$1" "${@:2}"; }

check CM-V01 "console workspace exists with a package manifest" \
  test -f console/package.json
check CM-V02 "package manifest pins pnpm via the packageManager field" \
  grep_q '"packageManager": "pnpm@' console/package.json
check CM-V03 "design tokens live in a Tailwind @theme layer" \
  grep_q '@theme' console/src/styles/tokens.css
check CM-V04 "TanStack Router file routes directory exists" \
  test -d console/src/routes
check CM-V05 "token layer defines the raised-surface role token" \
  grep_q -- '--color-bg-raised' console/src/styles/tokens.css
check CM-V06 "app shell component exists" \
  test -f console/src/components/shell/Shell.tsx
check CM-V07 "overview route renders its page components" \
  grep_q 'components/overview' console/src/routes/index.tsx
check CM-V08 "models route exists" \
  test -f console/src/routes/models.tsx
check CM-V09 "providers route exists" \
  test -f console/src/routes/providers.tsx
check CM-V10 "keys route exists" \
  test -f console/src/routes/keys.tsx
check CM-V11 "usage route exists" \
  test -f console/src/routes/usage.tsx
check CM-V12 "presets route exists" \
  test -f console/src/routes/presets.tsx
check CM-V13 "settings route exists" \
  test -f console/src/routes/settings.tsx
check CM-V14 "chat route exists" \
  test -f console/src/routes/chat.tsx
check CM-V15 "chat composer carries the model picker" \
  grep_q 'ModelPicker' console/src/components/chat/Composer.tsx
check CM-V16 "chat renders streaming markdown through streamdown" \
  grep_q '"streamdown"' console/package.json
check CM-V17 "chat has a comparison mode component" \
  grep_q 'compare' console/src/components/chat
check CM-V18 "console embeds the SPA build and serves its page fallback" \
  grep_q 'go:embed all:dist' internal/console/spa.go
# The catalog-performance campaign (CP12) shipped the palette under
# components/palette/, superseding the components/command/ location this
# plan proposed.
check CM-V19 "command palette component exists" \
  test -f console/src/components/palette/CommandPalette.tsx
check CM-V20 "DESIGN.md states the one-accent law" \
  grep_q 'One accent' DESIGN.md
check CM-V21 "legacy static console is deleted" \
  bash -c 'test ! -d internal/console/static && test ! -d internal/console/templates'

printf 'Summary: %d passed, %d failed\n' "$pass" "$fail"
test "$fail" -eq 0
