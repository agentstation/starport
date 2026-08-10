#!/usr/bin/env bash

set -euo pipefail

cask="${1:?usage: publish-homebrew-cask.sh CASK [REPOSITORY] [BRANCH]}"
repository="${2:-git@github.com:agentstation/homebrew-tap.git}"
branch="${3:-master}"

if [[ ! -f "$cask" ]]; then
	printf 'Homebrew cask is missing: %s\n' "$cask" >&2
	exit 1
fi

ruby -c "$cask" >/dev/null
version="$(ruby -e '
  text = File.read(ARGV.fetch(0))
  version = text[/^\s*version\s+"([^"]+)"/, 1]
  abort "cask version is missing" unless version
  print version
' "$cask")"

for attempt in 1 2 3; do
	checkout="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/starport-homebrew.XXXXXX")"
	if ! git clone --depth 1 --branch "$branch" "$repository" "$checkout"; then
		rm -rf "$checkout"
		exit 1
	fi

	mkdir -p "$checkout/Casks"
	install -m 0644 "$cask" "$checkout/Casks/starport.rb"
	if [[ -z "$(git -C "$checkout" status --short -- Casks/starport.rb)" ]]; then
		printf 'Homebrew tap already contains Starport %s\n' "$version"
		rm -rf "$checkout"
		exit 0
	fi

	git -C "$checkout" config user.name agentstation-release
	git -C "$checkout" config user.email releases@agentstation.ai
	git -C "$checkout" add Casks/starport.rb
	git -C "$checkout" commit -m "Update starport to v$version"
	if git -C "$checkout" push origin "HEAD:$branch"; then
		printf 'published Starport %s to the Homebrew tap\n' "$version"
		rm -rf "$checkout"
		exit 0
	fi

	rm -rf "$checkout"
	if [[ "$attempt" -lt 3 ]]; then
		printf 'Homebrew tap changed during publication; retrying (%s/3)\n' "$attempt" >&2
	fi
done

printf 'cannot publish Starport %s to the Homebrew tap after 3 attempts\n' "$version" >&2
exit 1
