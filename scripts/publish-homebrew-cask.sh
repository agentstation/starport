#!/usr/bin/env bash

set -euo pipefail

cask="${1:?usage: publish-homebrew-cask.sh CASK [REPOSITORY] [BRANCH]}"
repository="${2:-git@github.com:agentstation/homebrew-tap.git}"
branch="${3:-main}"

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
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	printf 'Homebrew publication requires a stable MAJOR.MINOR.PATCH version: %s\n' "$version" >&2
	exit 1
fi

compare_versions() {
	ruby - "$1" "$2" <<'RUBY'
require "rubygems"

print Gem::Version.new(ARGV.fetch(0)) <=> Gem::Version.new(ARGV.fetch(1))
RUBY
}

for attempt in 1 2 3; do
	checkout="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/starport-homebrew.XXXXXX")"
	if ! git clone --quiet --depth 1 --branch "$branch" "$repository" "$checkout"; then
		rm -rf "$checkout"
		exit 1
	fi

	mkdir -p "$checkout/Casks"
	current_cask="$checkout/Casks/starport.rb"
	if [[ -f "$current_cask" ]]; then
		current_version="$(ruby -e '
      text = File.read(ARGV.fetch(0))
      version = text[/^\s*version\s+"([^"]+)"/, 1]
      abort "tap cask version is missing" unless version
      print version
    ' "$current_cask")"
		if [[ ! "$current_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
			printf 'Homebrew tap contains an unsupported Starport version: %s\n' "$current_version" >&2
			rm -rf "$checkout"
			exit 1
		fi
		comparison="$(compare_versions "$current_version" "$version")"
		if ((comparison > 0)); then
			printf 'Homebrew tap already contains newer Starport %s; keeping it instead of %s\n' \
				"$current_version" "$version" >&2
			printf '%s\n' "$current_version"
			rm -rf "$checkout"
			exit 0
		fi
		if [[ "$current_version" == "$version" ]]; then
			if cmp -s "$cask" "$current_cask"; then
				printf 'Homebrew tap already contains Starport %s\n' "$version" >&2
				printf '%s\n' "$version"
				rm -rf "$checkout"
				exit 0
			fi
			printf 'Homebrew tap contains different content for Starport %s\n' "$version" >&2
			rm -rf "$checkout"
			exit 1
		fi
	fi

	install -m 0644 "$cask" "$current_cask"
	if [[ -z "$(git -C "$checkout" status --short -- Casks/starport.rb)" ]]; then
		printf 'Homebrew tap already contains Starport %s\n' "$version" >&2
		printf '%s\n' "$version"
		rm -rf "$checkout"
		exit 0
	fi

	git -C "$checkout" config user.name agentstation-release
	git -C "$checkout" config user.email releases@agentstation.ai
	git -C "$checkout" add Casks/starport.rb
	git -C "$checkout" commit --quiet -m "Update starport to v$version"
	if git -C "$checkout" push --quiet origin "HEAD:$branch"; then
		printf 'published Starport %s to the Homebrew tap\n' "$version" >&2
		printf '%s\n' "$version"
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
