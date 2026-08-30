# ENR-E1 proof: routing spread

Date: 2026-08-30. Branch: `codex/enr-e1`.

## What shipped

- `internal/routing/spread.go` owns the band and the weighted order.
  The band is the unbroken head of the sorted candidates that share
  the best route's rank tier. A candidate joins the band while its
  metric stays within the configured ratio of the best. The default
  ratio is 1.25. Inside the band the order is a weighted draw without
  replacement. The weight is inverse to the metric, so a cheaper or
  faster route still carries more traffic.
- The seed rides `OptimizationPolicy.SpreadSeed`, and the router
  draws it once per request. The same request with the same seed
  plans the same way twice, so the planner stays deterministic over
  its inputs.
- The metric mirrors the deterministic sort. It reads estimated cost
  first when the request prefers cost, then measured latency. A
  candidate with no metric ends the band. A zero best metric bounds
  the band at zero. Free routes then share traffic equally, and
  every priced route stays outside.
- The wire word is `provider.sort: "spread"`. The OpenRouter codec
  and the preset validator accept it. The console offers it in the
  chat composer and the preset editor. The router maps it onto the
  default ranking with the band turned on.
- The deterministic default stays unchanged. A request without spread
  keeps the exact plan it kept before this change.

## Acceptance evidence

- `TestPlanSpreadDistributesInsideTheBand` runs 1000 plans. Every
  in-band candidate leads at least once. The out-of-band candidate
  never leads, and it stays in the plan as the last fallback. The
  cheapest route leads more often than the dearest in-band route.
- `TestPlanWithoutSpreadStaysDeterministic` holds the default: two
  runs produce deeply equal plans in the deterministic order.
- `TestPlanSpreadSameSeedRepeatsThePlan` holds purity across 50
  seeds.
- `TestPlanSpreadBandEndsAtAnUnknownMetric` and
  `TestPlanSpreadZeroMetricBandHoldsFreeRoutes` hold the band
  boundaries.
- `bash scripts/verify-enterprise-readiness.sh`: `Summary: 18
  passed, 15 failed`. ENR-V18 turned green, the exact E1 condition.
  The 15 open conditions belong to later phases.
- `go test ./internal/routing/... -race`: PASS.

## Commands

- `go test ./...`: PASS.
- `go vet ./...`: PASS. `make lint`: 0 issues. `make build`: PASS.
- Console: `pnpm test` 210 passed across 33 files.
- The full `verify-*.sh` battery from the required evidence list:
  all structural gates PASS.

## Scope notes

- The ratio is a planner default, not yet an operator knob. The
  field `SpreadRatio` carries a per-request override for a future
  surface.
- Spread applies to the chat planning path through provider
  preferences. Operation-specific paths keep their fixed
  optimization policies.
