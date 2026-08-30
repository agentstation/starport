// Package guardrails owns the policy check contract this gateway runs
// against canonical requests and responses. A check reads canonical text,
// never a wire format, and answers one of three verdicts: allow, redact,
// or refuse. The ordered pipeline composes redactions and fails closed: a
// configured check that cannot evaluate refuses rather than waving the
// text through unread.
package guardrails

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Direction names which side of the exchange a check is reading.
type Direction string

const (
	// DirectionRequest marks text the caller sent.
	DirectionRequest Direction = "request"
	// DirectionResponse marks text a provider produced.
	DirectionResponse Direction = "response"
)

// Verdict is a check's answer about one piece of text.
type Verdict string

const (
	// VerdictAllow passes the text unchanged.
	VerdictAllow Verdict = "allow"
	// VerdictRedact passes the text with the flagged spans rewritten.
	VerdictRedact Verdict = "redact"
	// VerdictRefuse stops the request or withholds the response.
	VerdictRefuse Verdict = "refuse"
)

// Content is one piece of canonical text under inspection.
type Content struct {
	Direction Direction
	Text      string
}

// Result is one check's verdict. Redacted carries the rewritten text when
// the verdict is VerdictRedact. Reason says why for a refusal.
type Result struct {
	Verdict  Verdict
	Redacted string
	Reason   string
}

// Check inspects one piece of canonical text.
type Check interface {
	// Name identifies the check in configuration, refusals, and usage
	// records.
	Name() string
	// Inspect reads the text and answers a verdict. An error means the
	// check could not evaluate, which the pipeline treats as a refusal.
	Inspect(ctx context.Context, content Content) (Result, error)
}

// ErrRefused marks every guardrail refusal, so errors.Is finds one under
// any wrapping.
var ErrRefused = errors.New("guardrail refused")

// RefusalError is the refusal shape: which check refused, on which side,
// and why.
type RefusalError struct {
	Check     string
	Direction Direction
	Reason    string
}

func (e *RefusalError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("guardrail %s refused the %s", e.Check, e.Direction)
	}
	return fmt.Sprintf("guardrail %s refused the %s: %s", e.Check, e.Direction, e.Reason)
}

// Unwrap ties every refusal to ErrRefused.
func (e *RefusalError) Unwrap() error { return ErrRefused }

// Pipeline runs checks in registration order. Redactions compose: each
// check reads the text as the checks before it rewrote it.
type Pipeline struct {
	checks []Check
}

// NewPipeline builds a pipeline over the given checks, in order.
func NewPipeline(checks ...Check) *Pipeline {
	kept := make([]Check, 0, len(checks))
	for _, check := range checks {
		if check != nil {
			kept = append(kept, check)
		}
	}
	return &Pipeline{checks: kept}
}

// Len reports how many checks the pipeline runs. A nil pipeline runs
// none, so an unconfigured deployment never pays for this seam.
func (p *Pipeline) Len() int {
	if p == nil {
		return 0
	}
	return len(p.checks)
}

// Inspect runs every check over the text in order and answers the final
// text with the strongest verdict that occurred. A refusal stops the
// pipeline. So does a check error: per the fail-closed invariant, a
// configured check that cannot evaluate refuses, never allows.
func (p *Pipeline) Inspect(ctx context.Context, direction Direction, text string) (string, Verdict, error) {
	if p == nil {
		return text, VerdictAllow, nil
	}
	verdict := VerdictAllow
	for _, check := range p.checks {
		result, err := check.Inspect(ctx, Content{Direction: direction, Text: text})
		if err != nil {
			return "", VerdictRefuse, &RefusalError{
				Check:     check.Name(),
				Direction: direction,
				Reason:    fmt.Sprintf("check could not evaluate: %v", err),
			}
		}
		switch result.Verdict {
		case VerdictAllow:
		case VerdictRedact:
			text = result.Redacted
			verdict = VerdictRedact
		case VerdictRefuse:
			return "", VerdictRefuse, &RefusalError{
				Check:     check.Name(),
				Direction: direction,
				Reason:    result.Reason,
			}
		default:
			// An unnamed verdict is a check that cannot evaluate.
			return "", VerdictRefuse, &RefusalError{
				Check:     check.Name(),
				Direction: direction,
				Reason:    fmt.Sprintf("check answered unknown verdict %q", result.Verdict),
			}
		}
	}
	return text, verdict, nil
}

// Policy selects the pipeline that governs one account. Deployment-wide
// configuration answers the same pipeline for every account. A later
// per-account surface changes only the implementation behind this seam.
type Policy interface {
	PipelineFor(accountID string) *Pipeline
}

// StaticPolicy answers one pipeline for every account.
type StaticPolicy struct {
	Pipeline *Pipeline
}

// PipelineFor implements Policy.
func (p StaticPolicy) PipelineFor(string) *Pipeline { return p.Pipeline }

// builtins registers the checks configuration can name. The built-in
// checks task populates it.
var builtins = map[string]func() Check{}

// ErrUnknownCheck refuses a configured name this build does not ship.
var ErrUnknownCheck = errors.New("unknown guardrail check")

// BuildPipeline resolves configured check names into a pipeline. An
// unknown name is a startup error, not a silent skip: a deployment that
// asked for a check it cannot run must not serve traffic as if it ran.
func BuildPipeline(names []string) (*Pipeline, error) {
	checks := make([]Check, 0, len(names))
	for _, name := range names {
		constructor, ok := builtins[name]
		if !ok {
			return nil, fmt.Errorf("%w: %q (known checks: %s)", ErrUnknownCheck, name, knownCheckNames())
		}
		checks = append(checks, constructor())
	}
	return NewPipeline(checks...), nil
}

func knownCheckNames() string {
	if len(builtins) == 0 {
		return "none"
	}
	names := make([]string, 0, len(builtins))
	for name := range builtins {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
