#!/usr/bin/env bash
# Verify a README demonstration record against its manifest.
#
#   verify-readme-demo.sh --manifest docs/assets/starport-demo.json [--record DIR] [--json]
#
# Exit 0 is PASS, 1 is FAIL, and 2 is INVALID. The verifier needs only python3.

set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

exec python3 scripts/readme-demo/verify.py --root "$root" "$@"
