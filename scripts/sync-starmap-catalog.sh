#!/usr/bin/env bash

set -euo pipefail

module=github.com/agentstation/starmap
publisher=agentstation/starmap
manifest_path=internal/embedded/catalog/generation-manifest.json

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

gh api "repos/$publisher/contents/channel.json?ref=catalog/v2" \
	-H 'Accept: application/vnd.github.raw+json' >"$scratch/channel.json"
gh attestation verify "$scratch/channel.json" --repo "$publisher" \
	--signer-workflow "$publisher/.github/workflows/catalog-generation.yaml" \
	--source-ref refs/heads/main --deny-self-hosted-runners

source_commit=$(jq -er '.publication.source_commit | select(test("^[a-f0-9]{40}$"))' "$scratch/channel.json")
generation=$(jq -er '.generation_id | select(test("^[a-zA-Z0-9._:-]+$"))' "$scratch/channel.json")

module_manifest() {
	local directory
	directory=$(go mod download -json "$module@$1" | jq -er '.Dir')
	cat "$directory/$manifest_path"
}

current=$(go list -m -f '{{.Version}}' "$module")
module_manifest "$current" >"$scratch/current.json"
if jq -e --arg generation "$generation" '.generation_id == $generation' "$scratch/current.json" >/dev/null; then
	printf 'Starmap %s already embeds %s.\n' "$current" "$generation"
	exit 0
fi

module_manifest "$source_commit" >"$scratch/promoted.json"
jq -e --arg generation "$generation" '.generation_id == $generation' "$scratch/promoted.json" >/dev/null

if ! jq -es '
	def epoch: sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601;
	(.[1].generated_at | epoch) > (.[0].generated_at | epoch)
' "$scratch/current.json" "$scratch/promoted.json" >/dev/null; then
	printf 'The published Starmap generation %s is older than the current module baseline.\n' "$generation"
	exit 0
fi

go get "$module@$source_commit"
version=$(go list -m -f '{{.Version}}' "$module")
module_manifest "$version" >"$scratch/selected.json"
cmp "$scratch/promoted.json" "$scratch/selected.json"
git diff --exit-code -- . ':!go.mod' ':!go.sum'
if git diff --quiet -- go.mod; then
	printf 'The verified Starmap commit did not update go.mod.\n' >&2
	exit 1
fi

printf 'generation=%s\nversion=%s\n' "$generation" "$version" >>"$GITHUB_OUTPUT"
