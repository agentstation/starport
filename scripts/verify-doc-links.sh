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
  docs/DEPLOYMENT-TOPOLOGIES.md
  docs/OPERATOR-GUIDE.md
  docs/RECOVERY.md
  docs/SECURITY-POSTURE.md
  docs/README.md
  docs/TASKS.md
  docs/VERTEX_AI_CONFIG.md
  internal/config/README.md
)

# The documentation site tree is checked in full, so a new topic needs no
# entry here.
if [[ -d docs/site ]]; then
  while IFS= read -r file; do
    files+=("$file")
  done < <(find docs/site -type f -name '*.md' | LC_ALL=C sort)
fi

if (($# > 0)); then
  files=("$@")
fi

for file in "${files[@]}"; do
  if [[ ! -f "$file" ]]; then
    printf 'FAIL missing documentation file %s\n' "$file"
    exit 1
  fi
done

go run ./scripts/doclinks "${files[@]}"
