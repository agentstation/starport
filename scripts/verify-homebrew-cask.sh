#!/usr/bin/env bash

set -euo pipefail

cask="${1:-dist/homebrew/Casks/starport.rb}"
expected_version="${2:-}"

if [[ ! -f "$cask" ]]; then
	printf 'Homebrew cask is missing: %s\n' "$cask" >&2
	exit 1
fi

ruby -c "$cask" >/dev/null
actual_version="$(ruby -e '
  text = File.read(ARGV.fetch(0))
  version = text[/^\s*version\s+"([^"]+)"/, 1]
  abort "cask version is missing" unless version
  print version
' "$cask")"

require_text() {
	local pattern="$1"
	local description="$2"
	if ! grep -Eq "$pattern" "$cask"; then
		printf 'Homebrew cask is missing %s\n' "$description" >&2
		exit 1
	fi
}

require_text '^cask "starport" do$' 'the canonical cask name'
require_text '^[[:space:]]*binary "starport"$' 'the Starport binary artifact'
require_text '^[[:space:]]*manpage "manpages/starport\.1"$' 'the section-1 manual artifact'
require_text '^[[:space:]]*bash_completion "completions/starport\.bash"$' 'Bash completion'
require_text '^[[:space:]]*zsh_completion "completions/starport\.zsh"$' 'Zsh completion'
require_text '^[[:space:]]*fish_completion "completions/starport\.fish"$' 'Fish completion'
require_text '^[[:space:]]*on_arm do$' 'ARM archives'
require_text '^[[:space:]]*on_intel do$' 'x86-64 archives'
require_text '^[[:space:]]*on_macos do$' 'macOS archives'
require_text '^[[:space:]]*on_linux do$' 'Linux archives'
require_text '^[[:space:]]*postflight_steps do$' 'the structured post-install hook'

ruby -rripper - "$cask" <<'RUBY'
source = File.read(ARGV.fetch(0))
architecture = source[/^([ ]*)on_macos do\n\s*depends_on arch: :arm64\n\1end$/m]
abort "Homebrew cask must require Apple silicon on macOS" unless architecture
# Parse platform guards in either nesting order without executing the cask.
def call_name(node)
  return unless node.is_a?(Array)
  case node[0]
  when :command, :fcall then node[1][1]
  when :method_add_arg then call_name(node[1])
  end
end

def checksum_platforms(node, guards = [], result = [])
  return result unless node.is_a?(Array)
  if node[0] == :method_add_block
    name = call_name(node[1])
    nested = %w[on_macos on_linux on_arm on_intel].include?(name) ? guards + [name] : guards
    checksum_platforms(node[2], nested, result)
  elsif call_name(node) == "sha256"
    result << guards.sort
  else
    node.each { |child| checksum_platforms(child, guards, result) }
  end
  result
end

expected_platforms = [%w[on_arm on_macos], %w[on_arm on_linux], %w[on_intel on_linux]].map(&:sort).sort
abort "Homebrew cask must contain only Apple silicon macOS and ARM/x86-64 Linux archives" unless checksum_platforms(Ripper.sexp(source)).sort == expected_platforms
expected = <<~'HOOK'
  postflight_steps do
    on_macos do
      run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{staged_path}}/starport"], sudo: false
    end
  end
HOOK
hook = source[/^([ ]*)postflight_steps do\n.*?^\1end$/m]
normalized = hook&.lines&.map { |line| line.delete_prefix("  ") }&.join
abort "Homebrew cask must scope quarantine cleanup to the macOS Starport binary" unless normalized == expected.chomp
abort "Homebrew cask contains an unexpected quarantine operation" unless source.scan(/com\.apple\.quarantine/).length == 1
abort "Homebrew cask contains a legacy hook" if source.match?(/\b(?:postflight|system_command)\b/)
RUBY

if [[ -n "$expected_version" ]]; then
	if [[ "$actual_version" != "$expected_version" ]]; then
		printf 'Homebrew cask version is %s, want %s\n' "$actual_version" "$expected_version" >&2
		exit 1
	fi
fi

if grep -Eq 'sha256 "(no_check|[0]+)"' "$cask"; then
	printf 'Homebrew cask contains an invalid checksum\n' >&2
	exit 1
fi
if grep -Eq 'staged_path\.to_s|#\{staged_path\}["[:space:]]|sudo:[[:space:]]*true|HOMEBREW_PREFIX.*xattr|xattr.*HOMEBREW_PREFIX' "$cask"; then
	printf 'Homebrew cask quarantine removal is broader than the staged Starport binary\n' >&2
	exit 1
fi

printf 'PASS Homebrew cask syntax, platforms, checksums, installed artifacts, and scoped hook\n'
