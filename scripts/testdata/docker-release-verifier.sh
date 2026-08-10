#!/usr/bin/env bash

set -euo pipefail

printf '%s\n' "$*" >> "$STARPORT_TEST_DOCKER_LOG"

if [[ "$1 $2 $3" == "buildx imagetools inspect" ]]; then
	reference="$4"
	mode="$5"
	if [[ "$mode" == "--raw" ]]; then
		cat <<JSON
{"manifests":[
  {"digest":"$STARPORT_TEST_AMD64_DIGEST","platform":{"os":"linux","architecture":"amd64"}},
  {"digest":"$STARPORT_TEST_ARM64_DIGEST","platform":{"os":"linux","architecture":"arm64"}},
  {"digest":"$STARPORT_TEST_SBOM_ONE","platform":{"os":"unknown","architecture":"unknown"}},
  {"digest":"$STARPORT_TEST_SBOM_TWO","platform":{"os":"unknown","architecture":"unknown"}}
]}
JSON
	elif [[ "$reference" == *@"$STARPORT_TEST_AMD64_DIGEST" ]]; then
		printf '{"config":{"User":"65532:65532"}}\n'
	else
		printf '{"digest":"%s"}\n' "$STARPORT_TEST_INDEX_DIGEST"
	fi
	exit 0
fi

if [[ "$1" == "run" ]]; then
	printf 'starport version 1.2.3\n'
	exit 0
fi

printf 'unexpected docker invocation: %s\n' "$*" >&2
exit 1
