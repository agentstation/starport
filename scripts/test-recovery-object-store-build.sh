#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT

export PATH="$test_dir:$PATH"
export GOBIN="$test_dir/bin"
export FIXTURE_TEST_ATTEMPTS="$test_dir/attempts"

cat > "$test_dir/go" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
test "$GOSUMDB" = sum.golang.org
case "$1" in
	install)
		test "$2" = github.com/minio/minio@v0.0.0-20250907161309-07c3a429bfed
		printf 'install\n' >> "$FIXTURE_TEST_ATTEMPTS"
		case "$FIXTURE_TEST_MODE" in
			recover) if [[ $(wc -l < "$FIXTURE_TEST_ATTEMPTS") -gt 1 ]]; then exit 0; fi ;;
			checksum) printf 'SECURITY ERROR: checksum mismatch\n' >&2 ;;
		esac
		printf 'reading https://sum.golang.org/tile/8/0/123: stream error: INTERNAL_ERROR\n' >&2
		exit 1 ;;
	version)
		test "$2" = -m && test "$3" = "$GOBIN/minio"
		printf 'test build metadata\n' ;;
esac
SH
printf '#!/usr/bin/env bash\nexit 0\n' > "$test_dir/sleep"
chmod +x "$test_dir/go" "$test_dir/sleep"

evidence="$test_dir/build.txt"
FIXTURE_TEST_MODE=recover bash "$script_dir/build-recovery-object-store.sh" "$evidence"
test "$(wc -l < "$FIXTURE_TEST_ATTEMPTS")" -eq 2
grep -Fxq 'test build metadata' "$evidence"

rm "$evidence"
for mode in checksum exhausted; do
	: > "$FIXTURE_TEST_ATTEMPTS"
	if FIXTURE_TEST_MODE=$mode bash "$script_dir/build-recovery-object-store.sh" "$evidence"; then
		printf 'Expected fixture build failure in %s case.\n' "$mode" >&2
		exit 1
	fi
	expected=1
	if [[ "$mode" == exhausted ]]; then expected=3; fi
	test "$(wc -l < "$FIXTURE_TEST_ATTEMPTS")" -eq "$expected"
	test ! -e "$evidence"
done

printf 'PASS recovery fixture retry, limit, and checksum refusal\n'
