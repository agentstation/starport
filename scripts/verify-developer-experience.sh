#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
passed=0
failed=0

pass() {
  printf 'PASS %s\n' "$1"
  passed=$((passed + 1))
}

fail() {
  printf 'FAIL %s\n' "$1"
  failed=$((failed + 1))
}

require_file() {
  local id=$1
  local path=$2
  if [[ -f "$root/$path" ]]; then pass "$id"; else fail "$id"; fi
}

require_text() {
  local id=$1
  local pattern=$2
  local path=$3
  if [[ -f "$root/$path" ]] && grep -Eq -- "$pattern" "$root/$path"; then
    pass "$id"
  else
    fail "$id"
  fi
}

forbid_text() {
  local id=$1
  local pattern=$2
  shift 2
  if grep -EqR -- "$pattern" "$@"; then fail "$id"; else pass "$id"; fi
}

require_text DX-MAIN-1 'branches: \[main\]' .github/workflows/ci.yml
forbid_text DX-MAIN-2 'origin/master|fetch origin master' "$root/.github/workflows/release.yaml"
require_text DX-MAIN-3 'origin/main' .github/workflows/release.yaml

forbid_text DX-CFG-1 'os\.Setenv' "$root/internal/config"
require_text DX-CFG-2 'UserConfigDir' internal/config/paths.go
forbid_text DX-CFG-3 'default=\./data/starport' "$root/internal/config"
require_text DX-CFG-4 'default=127\.0\.0\.1' internal/config/config.go
require_text DX-CFG-5 'ENABLE_CORS,default=false' internal/config/config.go
require_text DX-CFG-6 'master key must be at least 32' internal/config/validation.go
require_text DX-CFG-7 'TestLoaderDoesNotMutateEnvironment' internal/config/loader_test.go

require_text DX-CLI-1 'github.com/urfave/cli/v3' go.mod
require_file DX-CLI-2 internal/cli/app.go
require_text DX-CLI-3 'Name:.*"init"' internal/cli/app.go
require_text DX-CLI-4 'Name:.*"doctor"' internal/cli/inspection_commands.go
require_text DX-CLI-5 'Name:.*"config"' internal/cli/inspection_commands.go
require_text DX-CLI-6 'Name:.*"completion"' internal/cli/app.go
require_text DX-CLI-7 'Name:.*"man"' internal/cli/app.go
require_text DX-CLI-8 '"json"' internal/cli/version.go
forbid_text DX-CLI-9 'log\.Fatalf|log\.Fatal\(' "$root/cmd/starport"
require_text DX-CLI-10 'CMD \["serve"\]' Dockerfile
require_text DX-CLI-11 'TestNoArgumentsShowHelp' internal/cli/app_test.go

require_file DX-SETUP-1 internal/setup/service.go
require_text DX-SETUP-2 '0600|0o600' internal/setup/service_test.go
require_text DX-SETUP-3 'TestInitializeCreatesNamedIdentity' internal/setup/service_test.go

require_file DX-AUTH-1 internal/providerauth/cloud.go
require_text DX-AUTH-2 'providerauth\.DefaultCloudChains' internal/config/loader.go
require_text DX-AUTH-3 'providerauth\.ProductionRegistry' internal/providers/connectors/authentication.go

require_text DX-BREW-1 '^homebrew_casks:' .goreleaser.yaml
require_text DX-BREW-2 'completions/\*' .goreleaser.yaml
require_text DX-BREW-3 'manpages/\*' .goreleaser.yaml
require_text DX-BREW-4 'Publish the generated Homebrew cask' .github/workflows/release.yaml
require_text DX-BREW-5 '^  verify-homebrew:' .github/workflows/release.yaml

forbid_text DX-DEV-1 '(^|[[:space:]])docker-compose[[:space:]]' "$root/Makefile" "$root/DEVELOPMENT.md"
forbid_text DX-DEV-2 '@latest' "$root/Makefile"
forbid_text DX-DEV-3 '^check:.*[[:space:]](format|fmt)([[:space:]]|$)' "$root/Makefile"
if awk '
  /^deps:/ { in_deps=1; next }
  in_deps && /^[^[:space:]#].*:/ { exit found ? 0 : 1 }
  in_deps && /go mod tidy/ { found=1 }
  END { if (in_deps) exit found ? 0 : 1 }
' "$root/Makefile"; then
  fail DX-DEV-4
else
  pass DX-DEV-4
fi
if awk '
  /^  valkey:/ { in_valkey=1; next }
  in_valkey && /^  [^[:space:]]/ { in_valkey=0 }
  in_valkey && /^    ports:/ { found=1 }
  END { exit found ? 0 : 1 }
' "$root/docker-compose.yml"; then
  fail DX-DEV-5
else
  pass DX-DEV-5
fi
require_text DX-DEV-6 '127\.0\.0\.1:\$\{STARPORT_VALKEY_PORT:-6379\}:6379' docker-compose.integration.yml
require_text DX-DOC-1 'brew install agentstation/tap/starport' README.md
forbid_text DX-DOC-2 'Coming Soon' "$root/docs/README.md"
require_file DX-DOC-3 scripts/smoke-first-run.sh
if "$root/scripts/verify-readme-quickstart.sh" >/dev/null; then
  pass DX-DOC-4
else
  fail DX-DOC-4
fi
if "$root/scripts/test-readme-quickstart-verifier.sh" >/dev/null; then
  pass DX-DOC-5
else
  fail DX-DOC-5
fi
require_text DX-DOC-6 '^    env_file:$' docker-compose.yml
require_text DX-DOC-7 '^        required: false$' docker-compose.yml
forbid_text DX-DOC-8 '^      [A-Z0-9_]+_API_KEY:' "$root/docker-compose.yml"

printf 'Summary: %d passed, %d failed\n' "$passed" "$failed"
test "$failed" -eq 0
