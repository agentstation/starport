#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
	echo "usage: capture-quality.sh STARPORT_REPOSITORY" >&2
	exit 2
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
starport_repository="$(cd "$1" && pwd)"
raw="$root/raw"
mkdir -p "$raw"

(
	cd "$starport_repository"
	go list -m -json all >"$raw/module-graph.json"
	go run github.com/google/go-licenses@v1.6.0 check ./cmd/starport \
		--allowed_licenses=AGPL-3.0,Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,MIT,MPL-2.0 \
		--ignore=github.com/agentstation/starport >"$raw/licenses.txt" 2>&1
	go run github.com/google/go-licenses@v1.6.0 csv ./cmd/starport \
		>"$raw/licenses.csv" 2>"$raw/licenses-csv-warnings.txt"
	go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./... >"$raw/govulncheck.txt" 2>&1
)
