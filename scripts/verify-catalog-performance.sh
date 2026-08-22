#!/usr/bin/env bash
# Guard for the catalog, performance, and brand campaign, whose durable
# plan closed on 2026-08-22. Each condition asserts one capability the
# campaign delivers. It reports every condition and exits
# nonzero while any condition fails. CI runs it as a required gate.
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
absent() { ! grep -q -- "$1" "$2"; }
either_file() {
  for candidate in "$@"; do
    test -f "$candidate" && return 0
  done
  return 1
}

# Phase A: projections and API surface
check CPV01 "authors endpoints are registered on the API router" \
  grep_q '/authors' internal/server/routes.go
check CPV02 "catalog presentation projection package exists with tests" \
  bash -c 'test -d internal/catalog/view && ls internal/catalog/view/*_test.go'
check CPV03 "model projection carries a per-provider offerings table" \
  grep_q 'json:"offerings' internal/catalog/view
check CPV04 "provider projection populates the description field" \
  grep_q 'provider.Description' internal/catalog/view/providers.go
check CPV05 "gateway serves catalog logos on a dedicated route" \
  grep_q '/logos/' internal/server/routes.go
check CPV06 "console renders identity through an EntityLogo fallback chain" \
  test -f console/src/components/catalog/EntityLogo.tsx

# Phase B: catalog traversal UX
# The detail page must not nest inside the list route (the list has no
# Outlet), so the TanStack un-nested spelling providers_. is canonical.
check CPV07 "provider detail route exists in the SPA" \
  either_file 'console/src/routes/providers_.$providerId.tsx' \
    'console/src/routes/providers.$providerId.tsx' \
    'console/src/routes/providers/$providerId.tsx'
check CPV08 "model detail route exists in the SPA" \
  either_file 'console/src/routes/models_.$modelId.tsx' \
    'console/src/routes/models.$modelId.tsx' \
    'console/src/routes/models/$modelId.tsx'
check CPV09 "author list and detail routes exist in the SPA" \
  bash -c 'test -f console/src/routes/authors.tsx &&
    { test -f "console/src/routes/authors_.\$authorId.tsx" ||
      test -f "console/src/routes/authors.\$authorId.tsx" ||
      test -f "console/src/routes/authors/\$authorId.tsx"; }'
check CPV10 "global command palette component exists" \
  test -f console/src/components/palette/CommandPalette.tsx

# Phase C: composer
check CPV11 "composer no longer embeds the presets popover" \
  absent 'listPresets' console/src/components/chat/Composer.tsx
check CPV12 "composer plus button owns attachments" \
  grep_q 'ttachment' console/src/components/chat/Composer.tsx

# Phase D: performance identity
check CPV13 "every proxied response carries the overhead header" \
  grep_q 'x-starport-overhead-ms' internal/
check CPV14 "usage surfaces the gateway overhead" \
  grep_q 'verhead' console/src/routes/usage.tsx
check CPV15 "chat metadata capitalizes TTFT" \
  grep_q 'TTFT' console/src/components/chat/Messages.tsx

# Phase E: brand
check CPV16 "sidebar wordmark reads STARPORT" \
  grep_q 'STARPORT' console/src/components/shell/Shell.tsx
check CPV17 "navigation labels the keys page API Keys" \
  grep_q 'API Keys' console/src/components/shell/Shell.tsx
check CPV18 "Starmap pin is v0.7.0 or later" \
  bash -c 'v=$(grep -o "starmap v[0-9.]*" go.mod | head -1 | cut -d" " -f2);
    test -n "$v" && printf "0.7.0\n%s\n" "${v#v}" | sort -V -C'

# Phase F: credential evidence
check CPV19 "credential state carries operator-facing detail" \
  bash -c 'grep -q "detail,omitempty" internal/providers/state/store.go &&
    grep -q "checkedCredentialEnvironment" internal/providers/reconciler.go &&
    grep -q "refreshFailures" internal/server/controllers/provider_operations.go'

# Phase G: failure normalization and cache honesty
check CPV20 "streaming provider rejections normalize and empty completions stay uncached" \
  bash -c 'grep -q "decodeStreamAPIError" internal/providers/connectors/openai_common.go &&
    grep -q "firstChunk, streamErr = stream.Recv()" internal/router/execution_adapter.go &&
    grep -q "ErrEmptyResponse" internal/response/cache/repository.go'

printf 'Summary: %d passed, %d failed\n' "$pass" "$fail"
test "$fail" -eq 0
