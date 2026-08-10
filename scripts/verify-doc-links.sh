#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

files=(
  README.md
  DEVELOPMENT.md
  MODELS.md
  SECURITY.md
  docs/ARCHITECTURE.md
  docs/CACHE_CONTROL.md
  docs/CONTRIBUTING.md
  docs/OPERATOR-GUIDE.md
  docs/README.md
  docs/TASKS.md
  docs/VERTEX_AI_CONFIG.md
  internal/config/README.md
)

if (($# > 0)); then
  files=("$@")
fi

for file in "${files[@]}"; do
  if [[ ! -f "$file" ]]; then
    printf 'FAIL missing documentation file %s\n' "$file"
    exit 1
  fi
done

records=$(mktemp "${TMPDIR:-/tmp}/starport-doc-links.XXXXXX")
trap 'rm -f "$records"' EXIT INT TERM
link_pattern='\[[^]]+\]\([^ )]+\)'
: >"$records"
for file in "${files[@]}"; do
  line_number=0
  while IFS= read -r content || [[ -n "$content" ]]; do
    line_number=$((line_number + 1))
    remainder=$content
    while [[ $remainder =~ $link_pattern ]]; do
      printf '%s:%d:%s\n' "$file" "$line_number" "${BASH_REMATCH[0]}" >>"$records"
      remainder=${remainder#*"${BASH_REMATCH[0]}"}
    done
  done <"$file"
done

failures=0
while IFS= read -r record; do
  file=${record%%:*}
  remainder=${record#*:}
  line=${remainder%%:*}
  link=${remainder#*:}
  target=${link#*](}
  target=${target%)}
  target=${target#<}
  target=${target%>}

  case "$target" in
    \#* | http://* | https://* | mailto:*) continue ;;
  esac

  target=${target%%#*}
  target=${target%%\?*}
  candidate=$(dirname "$file")/$target
  if [[ ! -e "$candidate" ]]; then
    printf 'FAIL %s:%s missing link target %s\n' "$file" "$line" "$target"
    failures=$((failures + 1))
  fi
done <"$records"

if ((failures > 0)); then
  printf 'Summary: %d broken documentation link(s)\n' "$failures"
  exit 1
fi

printf 'PASS documentation links\n'
