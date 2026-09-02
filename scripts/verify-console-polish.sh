#!/usr/bin/env bash
# Guard for the console polish campaign (ID prefix CPL).
# Each condition asserts one structural property of the target console:
#
#   - one query layer: query factories in lib/queries.ts, client defaults
#     in main.tsx, intent preload, a not-found component, abort signals,
#     and list state in search params,
#   - one primitive layer: shadcn on Base UI with dialog, sheet, popover,
#     dropdown menu, tooltip, skeleton, and command components, a theme
#     bootstrap, a skip link, toasts, a typed budget error, and no local
#     notice state,
#   - one data display layer: a shared DataTable, column scopes on every
#     plain table, price formatters with a pair form, synced charts with
#     endpoint emphasis,
#   - honest enterprise surfaces: build provenance, a webhook summary
#     route, observability and guardrail sections, budget meters, delete
#     dialogs, an audit until filter, guardrail and cache fields, an
#     export control, and a batches panel,
#   - page polish and small screens: sentence-case navigation, a model
#     picker on the key form, a chat default model test, real health
#     paths in the docs, and a media query hook.
#
# Authored red at baseline 6db57d8 (CPL-A0): each condition turns green as
# its CPL task closes. The gate is terminal at 48 conditions (CPL-V01
# through CPL-V48) and joins CI at CPL-Z1. It reports every condition and
# exits nonzero while any condition fails.
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 1

C=console/src
UI=$C/components/ui

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

# all_present reports that every term before the -- appears somewhere under
# the paths after it. A partial vocabulary is the failure this catches.
all_present() {
  local terms=() paths=()
  local seen=0 arg
  for arg in "$@"; do
    if [ "$arg" = "--" ]; then seen=1; continue; fi
    if [ "$seen" -eq 0 ]; then terms+=("$arg"); else paths+=("$arg"); fi
  done
  if [ "$seen" -eq 0 ] || [ "${#paths[@]}" -eq 0 ]; then return 1; fi
  local term
  for term in "${terms[@]}"; do
    grep -Rq -- "$term" "${paths[@]}" || return 1
  done
  return 0
}

# tests_all_present is all_present restricted to Go test files.
tests_all_present() {
  local terms=() paths=()
  local seen=0 arg
  for arg in "$@"; do
    if [ "$arg" = "--" ]; then seen=1; continue; fi
    if [ "$seen" -eq 0 ]; then terms+=("$arg"); else paths+=("$arg"); fi
  done
  if [ "$seen" -eq 0 ] || [ "${#paths[@]}" -eq 0 ]; then return 1; fi
  local term
  for term in "${terms[@]}"; do
    grep -Rq --include='*_test.go' -- "$term" "${paths[@]}" || return 1
  done
  return 0
}

# files_exist reports that every path exists.
files_exist() {
  local f
  for f in "$@"; do [ -e "$f" ] || return 1; done
  return 0
}

# absent reports that no path exists.
absent() {
  local f
  for f in "$@"; do [ -e "$f" ] && return 1; done
  return 0
}

# count_at_least reports that the matching lines for a term across the
# paths reach a floor.
count_at_least() {
  local floor="$1" term="$2"
  shift 2
  local n
  n=$(cat "$@" 2>/dev/null | grep -c -- "$term")
  [ "$n" -ge "$floor" ]
}

# zero_matches reports that no line under the path carries the term.
zero_matches() {
  local term="$1" path="$2"
  local n
  n=$(grep -R -- "$term" "$path" 2>/dev/null | wc -l | tr -d ' ')
  [ "$n" -eq 0 ]
}

# every_query_signals reports that lib/queries.ts passes a signal from
# every query function it defines: every line that declares a queryFn
# takes the signal on that line, and there is at least one.
every_query_signals() {
  local f=$C/lib/queries.ts
  [ -f "$f" ] || return 1
  local fns sigs
  fns=$(grep -c 'queryFn' "$f")
  sigs=$(grep -c 'queryFn.*signal' "$f")
  [ "$fns" -gt 0 ] && [ "$fns" -eq "$sigs" ]
}

# routes_with_search reports that at least eleven route files validate
# search params.
routes_with_search() {
  local n
  n=$(grep -l validateSearch $C/routes/*.tsx 2>/dev/null | grep -vc '\.test\.')
  [ "$n" -ge 11 ]
}

# --- Phase B, data layer ---

check CPL-V01 "lib/queries.ts owns the query factories through queryOptions" \
  all_present 'queryOptions' -- $C/lib/queries.ts

check CPL-V02 "main.tsx sets the query client defaults" \
  all_present 'defaultOptions' -- $C/main.tsx

check CPL-V03 "the router preloads on intent" \
  all_present 'defaultPreload' -- $C/main.tsx $C/router.tsx $C/routes/__root.tsx

check CPL-V04 "the router owns a not-found component" \
  all_present 'defaultNotFoundComponent' -- $C/main.tsx $C/router.tsx $C/routes/__root.tsx

check CPL-V05 "every factory in lib/queries.ts passes the query signal" \
  every_query_signals

check CPL-V06 "at least eleven route files validate search params" \
  routes_with_search

# --- Phase C, primitives ---

check CPL-V07 "the console holds a shadcn components manifest" \
  files_exist console/components.json

check CPL-V08 "the console depends on Base UI" \
  all_present '"@base-ui/react"' -- console/package.json

check CPL-V09 "index.html bootstraps the theme before paint" \
  all_present 'data-theme' -- console/index.html

check CPL-V10 "the shell renders a skip link" \
  all_present 'Skip to' -- $C/components/shell/Shell.tsx

check CPL-V11 "dialog.tsx is present and Modal.tsx is gone" \
  bash -c "[ -f $UI/dialog.tsx ] && [ ! -e $UI/Modal.tsx ]"

check CPL-V12 "sheet.tsx is present and SidePanel.tsx is gone" \
  bash -c "[ -f $UI/sheet.tsx ] && [ ! -e $UI/SidePanel.tsx ]"

check CPL-V13 "Form.tsx owns DestructiveButton" \
  all_present 'DestructiveButton' -- $UI/Form.tsx

check CPL-V14 "the console depends on sonner for toasts" \
  all_present '"sonner"' -- console/package.json

check CPL-V15 "lib/api.ts types the budget exhausted error" \
  all_present 'budgetExhausted' -- $C/lib/api.ts

check CPL-V16 "no component keeps its own notice state" \
  zero_matches 'setNotice(' $C

check CPL-V17 "tooltip and dropdown menu components exist" \
  files_exist $UI/tooltip.tsx $UI/dropdown-menu.tsx

check CPL-V18 "the chat model picker sets aria-activedescendant" \
  all_present 'aria-activedescendant' -- $C/components/chat/ModelPicker.tsx

check CPL-V19 "a skeleton component exists" \
  files_exist $UI/skeleton.tsx

check CPL-V20 "Field sets aria-invalid on error" \
  all_present 'aria-invalid' -- $UI/Form.tsx

check CPL-V21 "chat messages carry a live region" \
  all_present 'aria-live' -- $C/components/chat/Messages.tsx

# --- Phase D, data display ---

check CPL-V22 "format.test.ts covers the price pair formatter" \
  all_present 'formatPricePair' -- $C/lib/format.test.ts

check CPL-V23 "format.ts exports formatPricePair" \
  all_present 'export function formatPricePair' -- $C/lib/format.ts

check CPL-V24 "components/ui owns a shared DataTable" \
  files_exist $UI/DataTable.tsx

check CPL-V25 "the audit route uses DataTable" \
  all_present 'DataTable' -- $C/routes/audit.tsx

check CPL-V26 "provider offerings use DataTable" \
  all_present 'DataTable' -- $C/components/providers/ProviderDetail.tsx

check CPL-V27 "the seven plain tables carry column scopes" \
  count_at_least 7 'scope="col"' \
  $C/components/models/ModelDetail.tsx \
  $C/components/documents/DocumentsPanel.tsx \
  $C/components/files/FilesPanel.tsx \
  $C/components/jobs/JobsPanel.tsx \
  $C/components/models/ChangesPanel.tsx \
  $C/routes/accounts.tsx \
  $C/routes/teams.tsx

check CPL-V28 "the usage charts share a syncId" \
  all_present 'syncId' -- $UI/Chart.tsx $C/routes/usage.tsx

check CPL-V29 "the chart emphasizes the last point with ReferenceDot" \
  all_present 'ReferenceDot' -- $UI/Chart.tsx $C/routes/usage.tsx

# --- Phase E, enterprise surfaces ---

check CPL-V30 "system info reports build provenance, held by tests" \
  tests_all_present 'TestSystemInfoReportsBuildVersion' -- internal/server/controllers

check CPL-V31 "the server mounts the admin webhook summary route" \
  all_present 'admin/webhooks' -- internal/server

check CPL-V32 "settings panels render Observability and Guardrails sections" \
  all_present 'Observability' 'Guardrails' -- $C/components/settings

check CPL-V33 "components/ui owns a shared BudgetLine" \
  files_exist $UI/BudgetLine.tsx

check CPL-V34 "a team read includes budget usage, held by tests" \
  tests_all_present 'TestTeamReadIncludesBudgetUsage' -- internal/server

check CPL-V35 "delete team and delete account dialogs exist" \
  bash -c "[ -n \"\$(find $C -name DeleteTeamModal.tsx)\" ] && [ -n \"\$(find $C -name DeleteAccountModal.tsx)\" ]"

check CPL-V36 "the audit route and client filters carry until" \
  bash -c "grep -q 'until' $C/routes/audit.tsx && grep -q 'until' $C/lib/api.ts"

check CPL-V37 "lib/api.ts types the guardrail verdict field" \
  all_present 'guardrail_verdict' -- $C/lib/api.ts

check CPL-V38 "the usage route offers an NDJSON export" \
  all_present 'ndjson' -- $C/routes/usage.tsx

check CPL-V39 "the console names the X-Semantic-Cache header" \
  all_present 'X-Semantic-Cache' -- $C

check CPL-V40 "a batches panel exists" \
  files_exist $C/components/jobs/BatchesPanel.tsx

# --- Phase F, page polish ---

check CPL-V41 "the shell navigation reads Audit log in sentence case" \
  all_present 'Audit log' -- $C/components/shell/Shell.tsx

check CPL-V42 "the key form uses the model picker" \
  all_present 'ModelPicker' -- $C/routes/keys.tsx

check CPL-V43 "chat.test.tsx exists" \
  files_exist $C/routes/chat.test.tsx

check CPL-V44 "the docs cite the real health paths" \
  all_present '/health/live' '/health/ready' -- $C/routes/docs.tsx

check CPL-V45 "docs.test.tsx exists" \
  files_exist $C/routes/docs.test.tsx

# --- Phase G, small screens ---

check CPL-V46 "lib/useMediaQuery.ts exists" \
  files_exist $C/lib/useMediaQuery.ts

# --- Close ---

check CPL-V47 "CI runs this gate" \
  all_present 'verify-console-polish.sh' -- .github/workflows

check CPL-V48 "the AGENTS.md evidence list names this gate" \
  all_present 'verify-console-polish.sh' -- AGENTS.md

printf 'Summary: %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
