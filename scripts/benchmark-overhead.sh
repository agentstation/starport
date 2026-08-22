#!/usr/bin/env bash
# Gateway overhead benchmark gate. Runs the chat pipeline against a mock
# upstream and fails when the p99 gateway-added latency exceeds the bound
# published in README.md. Methodology: docs/PERFORMANCE.md.
set -euo pipefail

cd "$(dirname "$0")/.."

STARPORT_OVERHEAD_BENCH=1 go test -run TestGatewayOverheadBenchmark -v \
  ./internal/server/controllers/ | grep -E "overhead|PASS|FAIL|ok " || {
  echo "FAIL gateway overhead benchmark"
  exit 1
}
