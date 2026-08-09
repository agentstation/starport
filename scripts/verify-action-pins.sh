#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workflow_directory="$repository_root/.github/workflows"
api_token="${GITHUB_TOKEN:-${GH_TOKEN:-}}"

if [ -z "$api_token" ] && command -v gh >/dev/null 2>&1; then
	api_token="$(gh auth token 2>/dev/null || true)"
fi

for required_tool in curl jq; do
	if ! command -v "$required_tool" >/dev/null 2>&1; then
		printf 'verify-action-pins requires %s\n' "$required_tool" >&2
		exit 1
	fi
done

github_api() {
	local api_path="$1"
	if [ -n "$api_token" ]; then
		curl -fsS --retry 4 --retry-all-errors --retry-delay 2 --max-time 30 \
			-H 'Accept: application/vnd.github+json' \
			-H 'X-GitHub-Api-Version: 2022-11-28' \
			-H "Authorization: Bearer ${api_token}" \
			"https://api.github.com${api_path}"
		return
	fi
	curl -fsS --retry 4 --retry-all-errors --retry-delay 2 --max-time 30 \
		-H 'Accept: application/vnd.github+json' \
		-H 'X-GitHub-Api-Version: 2022-11-28' \
		"https://api.github.com${api_path}"
}

pin_file="$(mktemp "${TMPDIR:-/tmp}/starport-action-pins.XXXXXX")"
trap 'rm -f "$pin_file"' EXIT

unverifiable="$({ grep -hE '^[[:space:]]*uses:' "$workflow_directory"/*.yml "$workflow_directory"/*.yaml 2>/dev/null || true; } | grep -vE 'uses:[[:space:]]+\./|uses:[[:space:]]+[^@[:space:]]+@[0-9a-f]{40}[[:space:]]*#[[:space:]]*\S+' || true)"
if [ -n "$unverifiable" ]; then
	printf 'workflow actions without a commit pin and version comment:\n%s\n' \
		"$unverifiable" >&2
	exit 1
fi

{ grep -hoE 'uses:[[:space:]]+[^@[:space:]]+@[0-9a-f]{40}[[:space:]]*#[[:space:]]*\S+' "$workflow_directory"/*.yml "$workflow_directory"/*.yaml 2>/dev/null || true; } |
	sed -E 's/^uses:[[:space:]]+//; s/@/ /; s/[[:space:]]*#[[:space:]]*/ /' |
	LC_ALL=C sort -u >"$pin_file"

if [ ! -s "$pin_file" ]; then
	printf 'no SHA-pinned actions found under %s\n' "$workflow_directory" >&2
	exit 1
fi

failure_count=0
checked_count=0
while read -r action pinned_commit release_tag; do
	[ -z "$action" ] && continue
	action_repository="$(printf '%s\n' "$action" | cut -d/ -f1,2)"

	if ! tag_ref="$(github_api "/repos/${action_repository}/git/ref/tags/${release_tag}" 2>/dev/null)"; then
		printf '%s: cannot resolve tag %s in %s\n' \
			"$action" "$release_tag" "$action_repository" >&2
		failure_count=$((failure_count + 1))
		continue
	fi

	object_type="$(jq -r '.object.type // empty' <<<"$tag_ref")"
	resolved_commit="$(jq -r '.object.sha // empty' <<<"$tag_ref")"
	if [ "$object_type" = tag ]; then
		if ! tag_object="$(github_api "/repos/${action_repository}/git/tags/${resolved_commit}" 2>/dev/null)"; then
			printf '%s: cannot dereference annotated tag %s\n' \
				"$action" "$release_tag" >&2
			failure_count=$((failure_count + 1))
			continue
		fi
		resolved_commit="$(jq -r '.object.sha // empty' <<<"$tag_object")"
	fi

	if [ "$resolved_commit" != "$pinned_commit" ]; then
		printf '%s pinned to %s but %s resolves to %s\n' \
			"$action" "$pinned_commit" "$release_tag" "$resolved_commit" >&2
		failure_count=$((failure_count + 1))
		continue
	fi

	checked_count=$((checked_count + 1))
	printf '%s %s matches %s\n' "$action" "$release_tag" "$pinned_commit"
done <"$pin_file"

if [ "$failure_count" -gt 0 ]; then
	printf '%s action pin(s) do not match their release tags\n' \
		"$failure_count" >&2
	exit 1
fi

printf 'action pins: %s reference(s) match their release tags\n' \
	"$checked_count"
