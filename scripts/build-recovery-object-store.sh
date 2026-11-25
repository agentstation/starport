#!/usr/bin/env bash

set -euo pipefail

fixture='github.com/minio/minio@v0.0.0-20250907161309-07c3a429bfed'
evidence_path=$1
export GOSUMDB=sum.golang.org
: "${GOBIN:?Set GOBIN to the recovery fixture directory}"

log=$(mktemp)
trap 'rm -f "$log"' EXIT

for attempt in 1 2 3; do
	if go install "$fixture" 2>&1 | tee "$log"; then
		go version -m "$GOBIN/minio" > "$evidence_path"
		exit 0
	fi
	if [[ "$attempt" == 3 ]] ||
		grep -Eq 'checksum mismatch|SECURITY ERROR' "$log" ||
		! grep -Eq 'https://(sum|proxy)\.golang\.org/.*(stream error|connection reset|TLS handshake timeout|i/o timeout|50[234] )' "$log"; then
		exit 1
	fi
	printf 'Retry Go module downloads after attempt %s of 3.\n' "$attempt" >&2
	sleep 5
done
