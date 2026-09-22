#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
command -v pnpm >/dev/null 2>&1 || {
	printf 'pnpm is required to build the Starport console\n' >&2
	exit 1
}
pnpm -C "$repository_root/console" install --frozen-lockfile
pnpm -C "$repository_root/console" build
test -s "$repository_root/internal/console/dist/index.html"
