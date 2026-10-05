#!/usr/bin/env bash
# Record a README demonstration rehearsal, or add the pacing review to a record.
#
# Rehearse:
#   record-readme-demo.sh --rehearse --output DIR --run ID
#   record-readme-demo.sh --rehearse --output DIR --candidate-archive PATH
#       [--candidate-checksums FILE] [--candidate-head SHA] [--font PATH]
# Review:
#   record-readme-demo.sh --review DIR --reviewer NAME --pacing-verdict PASS|FAIL
#       [--notes TEXT]
#
# A rehearsal needs macOS on ARM64, python3 with Pillow, and sandbox-exec. The
# capture runs in a sandbox that refuses network access except loopback. Only
# the --run candidate step uses the network, through gh.

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
demo="$repository_root/scripts/readme-demo"
# The capture may connect only to loopback addresses.
sandbox_profile='(version 1)(allow default)(deny network-outbound (remote ip "*:*"))(allow network-outbound (remote ip "localhost:*"))'

usage() {
	sed -n '2,14p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
	exit 2
}

fail() {
	printf 'record-readme-demo: %s\n' "$1" >&2
	exit 1
}

mode=""
output=""
run_id=""
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

[[ $mode == rehearse ]] || usage
[[ -n $output ]] || fail "--output is required"
if [[ -n $run_id && -n $archive ]] || [[ -z $run_id && -z $archive ]]; then
	fail "select one candidate: --run or --candidate-archive"
fi
[[ $(uname -s) == Darwin && $(uname -m) == arm64 ]] || fail "a rehearsal needs macOS on ARM64"
[[ -x /usr/bin/sandbox-exec ]] || fail "a rehearsal needs /usr/bin/sandbox-exec"
python3 -c 'import PIL' 2>/dev/null || fail "a rehearsal needs python3 with Pillow"
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
if [[ -n $run_id ]]; then
	candidate+=(--run "$run_id")
else
	candidate+=(--archive "$archive")
	[[ -z $checksums ]] || candidate+=(--checksums "$checksums")
	[[ -z $head ]] || candidate+=(--head "$head")
fi
"${candidate[@]}"

/usr/bin/sandbox-exec -p "$sandbox_profile" \
	python3 "$demo/capture.py" capture --work "$work" --repository "$repository_root"
printf '\n'

render=(python3 "$demo/render.py" --work "$work" --output "$output")
[[ -z $font ]] || render+=(--font "$font")
"${render[@]}"
printf 'Rehearsal recorded in %s. Watch first-use.gif, then run --review.\n' "$output"
