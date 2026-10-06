#!/usr/bin/env bash
# Record a README demonstration rehearsal or release, or add the pacing review
# to a record.
#
# Rehearse:
#   record-readme-demo.sh --rehearse --output DIR --run ID
#   record-readme-demo.sh --rehearse --output DIR --candidate-archive PATH
#       [--candidate-checksums FILE] [--candidate-head SHA] [--font PATH]
# Release:
#   OPENAI_API_KEY=... record-readme-demo.sh --release TAG --output DIR [--font PATH]
# Review:
#   record-readme-demo.sh --review DIR --reviewer NAME --pacing-verdict PASS|FAIL
#       [--notes TEXT]
#
# A recording needs macOS on ARM64 and python3 with Pillow. A rehearsal also
# needs sandbox-exec: its capture runs in a sandbox that refuses network access
# except loopback, and a local fixture answers. Only the --run candidate step
# uses the network, through gh.
#
# A release recording downloads the darwin arm64 archive of the published
# release with gh, verifies its checksum and its attestation, and answers from
# the real OpenAI provider. Its capture runs without the sandbox, because the
# provider needs network access. The capture reads OPENAI_API_KEY from this
# environment and never prints or stores the value. The record carries
# kind: release and qualifies the release cases.

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
demo="$repository_root/scripts/readme-demo"
# The capture may connect only to loopback addresses.
sandbox_profile='(version 1)(allow default)(deny network-outbound (remote ip "*:*"))(allow network-outbound (remote ip "localhost:*"))'

usage() {
	sed -n '2,25p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
	exit 2
}

fail() {
	printf 'record-readme-demo: %s\n' "$1" >&2
	exit 1
}

mode=""
output=""
run_id=""
release_tag=""
archive=""
checksums=""
head=""
font=""
review=""
reviewer=""
verdict=""
notes=""
while (($#)); do
	case "$1" in
	--rehearse) mode=rehearse ;;
	--release)
		mode=release
		release_tag="${2:?}"
		shift
		;;
	--review)
		mode=review
		review="${2:?}"
		shift
		;;
	--output)
		output="${2:?}"
		shift
		;;
	--run)
		run_id="${2:?}"
		shift
		;;
	--candidate-archive)
		archive="${2:?}"
		shift
		;;
	--candidate-checksums)
		checksums="${2:?}"
		shift
		;;
	--candidate-head)
		head="${2:?}"
		shift
		;;
	--font)
		font="${2:?}"
		shift
		;;
	--reviewer)
		reviewer="${2:?}"
		shift
		;;
	--pacing-verdict)
		verdict="${2:?}"
		shift
		;;
	--notes)
		notes="${2:?}"
		shift
		;;
	-h | --help) usage ;;
	*) usage ;;
	esac
	shift
done

if [[ $mode == review ]]; then
	[[ -f "$review/record.json" ]] || fail "the record directory has no record.json: $review"
	[[ -n $reviewer ]] || fail "--reviewer is required"
	[[ $verdict == PASS || $verdict == FAIL ]] || fail "--pacing-verdict must be PASS or FAIL"
	# record.json is not a hashed output, so the review leaves every output hash valid.
	python3 - "$review/record.json" "$reviewer" "$verdict" "$notes" <<'PYTHON'
import datetime
import json
import sys
from pathlib import Path

path, reviewer, verdict, notes = Path(sys.argv[1]), *sys.argv[2:]
record = json.loads(path.read_text())
record["human_review"] = {"reviewer": reviewer, "date": datetime.date.today().isoformat(),
                          "pacing_verdict": verdict, "notes": notes or None}
path.write_text(json.dumps(record, indent=2) + "\n")
print(json.dumps(record["human_review"]))
PYTHON
	exit 0
fi

[[ $mode == rehearse || $mode == release ]] || usage
[[ -n $output ]] || fail "--output is required"
if [[ $mode == release ]]; then
	[[ -z $run_id && -z $archive && -z $checksums && -z $head ]] || fail "--release takes no rehearsal candidate option"
	[[ $release_tag == v[0-9]*.[0-9]*.[0-9]* ]] || fail "--release needs a tag of the form vX.Y.Z"
	command -v gh >/dev/null 2>&1 || fail "--release needs the gh CLI"
	[[ -n ${OPENAI_API_KEY:-} ]] || fail "--release needs OPENAI_API_KEY in the environment"
elif [[ -n $run_id && -n $archive ]] || [[ -z $run_id && -z $archive ]]; then
	fail "select one candidate: --run or --candidate-archive"
fi
[[ $(uname -s) == Darwin && $(uname -m) == arm64 ]] || fail "a recording needs macOS on ARM64"
python3 -c 'import PIL' 2>/dev/null || fail "a recording needs python3 with Pillow"
if [[ $mode == rehearse ]]; then
	[[ -x /usr/bin/sandbox-exec ]] || fail "a rehearsal needs /usr/bin/sandbox-exec"
fi
if [[ -n $run_id ]]; then
	command -v gh >/dev/null 2>&1 || fail "--run needs the gh CLI"
fi
if [[ -e $output ]] && [[ -n "$(ls -A "$output")" ]]; then
	fail "the output directory is not empty: $output"
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/starport-readme-demo.XXXXXX")"
cleanup() {
	# The work directory holds the downloaded archive, so it never outlives the run.
	if [[ ${work##*/} == starport-readme-demo.* ]]; then
		rm -rf -- "$work"
	fi
}
trap cleanup EXIT INT TERM

candidate=(python3 "$demo/capture.py" candidate --work "$work")
if [[ $mode == release ]]; then
	candidate+=(--release-tag "$release_tag")
elif [[ -n $run_id ]]; then
	candidate+=(--run "$run_id")
else
	candidate+=(--archive "$archive")
	[[ -z $checksums ]] || candidate+=(--checksums "$checksums")
	[[ -z $head ]] || candidate+=(--head "$head")
fi
"${candidate[@]}"

if [[ $mode == release ]]; then
	# The real provider needs network access, so the release capture runs
	# without the loopback-only sandbox.
	python3 "$demo/capture.py" capture --work "$work" --repository "$repository_root"
else
	/usr/bin/sandbox-exec -p "$sandbox_profile" \
		python3 "$demo/capture.py" capture --work "$work" --repository "$repository_root"
fi
printf '\n'

render=(python3 "$demo/render.py" --work "$work" --output "$output")
[[ -z $font ]] || render+=(--font "$font")
"${render[@]}"
if [[ $mode == release ]]; then
	printf 'Release %s recorded in %s. Watch first-use.gif, then run --review.\n' "$release_tag" "$output"
else
	printf 'Rehearsal recorded in %s. Watch first-use.gif, then run --review.\n' "$output"
fi
