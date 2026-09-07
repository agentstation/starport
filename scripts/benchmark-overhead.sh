#!/usr/bin/env bash
# Guard the controller timer against its component threshold.
# Measurement boundaries: docs/PERFORMANCE.md.
set -euo pipefail

cd "$(dirname "$0")/.."

STARPORT_OVERHEAD_BENCH=1 go test -run TestGatewayOverheadBenchmark -v \
  ./internal/server/controllers/ | grep -E "overhead|PASS|FAIL|ok " || {
  echo "FAIL gateway overhead benchmark"
  exit 1
}
