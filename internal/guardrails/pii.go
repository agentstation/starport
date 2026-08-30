package guardrails

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The pii check detects deterministic personal identifiers: email
// addresses, phone numbers, card numbers under Luhn, and US SSNs. It runs
// no model. Configuration picks what a finding does: redact rewrites each
// identifier to a bracketed label, refuse stops the exchange and names
// the categories it found.

// PII modes configuration can pick.
const (
	// PIIModeRedact rewrites each identifier to a bracketed label.
	PIIModeRedact = "redact"
	// PIIModeRefuse stops the exchange and names the categories found.
	PIIModeRefuse = "refuse"
)

// piiCheckName is the registered name of the PII check.
const piiCheckName = "pii"

var (
	// emailPattern matches one mailbox address.
	emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9][A-Za-z0-9.-]*\.[A-Za-z]{2,}`)
	// cardPattern matches a run of 13 through 19 digits with optional
	// single space or dash separators. Luhn decides whether the run is a
	// card number; the pattern only nominates candidates.
	cardPattern = regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`)
	// ssnPattern matches the dashed US SSN form. The dashes are required:
	// a bare nine-digit run reads as any other number.
	ssnPattern = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
	// phonePattern matches North American numbers written with separators
	// and E.164 international numbers. A separator or a leading plus is
	// required, so a bare digit run stays untouched.
	phonePattern = regexp.MustCompile(`(?:\+1[ .-]?)?\(?\b\d{3}\)?[ .-]\d{3}[ .-]\d{4}\b|\+[1-9]\d{6,14}\b`)
)

// piiCheck is the deterministic PII detector.
type piiCheck struct {
	refuse bool
}

// newPIICheck builds the check from settings. An empty mode redacts.
func newPIICheck(settings Settings) (Check, error) {
	switch settings.PIIMode {
	case "", PIIModeRedact:
		return &piiCheck{}, nil
	case PIIModeRefuse:
		return &piiCheck{refuse: true}, nil
	default:
		return nil, fmt.Errorf("unknown pii mode %q (pick %s or %s)", settings.PIIMode, PIIModeRedact, PIIModeRefuse)
	}
}

// Name implements Check.
func (c *piiCheck) Name() string { return piiCheckName }

// Inspect implements Check.
func (c *piiCheck) Inspect(_ context.Context, content Content) (Result, error) {
	spans := findPIISpans(content.Text)
	if len(spans) == 0 {
		return Result{Verdict: VerdictAllow}, nil
	}
	if c.refuse {
		return Result{
			Verdict: VerdictRefuse,
			Reason:  fmt.Sprintf("personal identifiers found: %s", spanCategories(spans)),
		}, nil
	}
	return Result{Verdict: VerdictRedact, Redacted: redactSpans(content.Text, spans)}, nil
}

// piiSpan is one detected identifier: where it sits and what it is.
type piiSpan struct {
	start, end int
	category   string
}

// findPIISpans runs every detector and keeps non-overlapping spans in
// text order. Where two detectors claim the same bytes, the earlier and
// then the longer span wins, so a card number is never re-read as a
// phone number's fragment.
func findPIISpans(text string) []piiSpan {
	var spans []piiSpan
	collect := func(pattern *regexp.Regexp, category string, valid func(string) bool) {
		for _, match := range pattern.FindAllStringIndex(text, -1) {
			if valid != nil && !valid(text[match[0]:match[1]]) {
				continue
			}
			spans = append(spans, piiSpan{start: match[0], end: match[1], category: category})
		}
	}
	collect(emailPattern, "email", nil)
	collect(cardPattern, "card", cardNumberUnderLuhn)
	collect(ssnPattern, "ssn", validSSN)
	collect(phonePattern, "phone", nil)
	sort.Slice(spans, func(left, right int) bool {
		if spans[left].start != spans[right].start {
			return spans[left].start < spans[right].start
		}
		return spans[left].end > spans[right].end
	})
	kept := spans[:0]
	lastEnd := 0
	for _, span := range spans {
		if span.start < lastEnd {
			continue
		}
		kept = append(kept, span)
		lastEnd = span.end
	}
	return kept
}

// redactSpans rewrites each span to a bracketed category label.
func redactSpans(text string, spans []piiSpan) string {
	var builder strings.Builder
	last := 0
	for _, span := range spans {
		builder.WriteString(text[last:span.start])
		builder.WriteString("[redacted-")
		builder.WriteString(span.category)
		builder.WriteString("]")
		last = span.end
	}
	builder.WriteString(text[last:])
	return builder.String()
}

// spanCategories names the distinct categories found, sorted.
func spanCategories(spans []piiSpan) string {
	seen := map[string]bool{}
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		if !seen[span.category] {
			seen[span.category] = true
			names = append(names, span.category)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// cardNumberUnderLuhn reports whether a candidate digit run is a card
// number: 13 through 19 digits whose Luhn checksum holds.
func cardNumberUnderLuhn(candidate string) bool {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, candidate)
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		digit := int(digits[i] - '0')
		if double {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		double = !double
	}
	return sum%10 == 0
}

// validSSN drops the number ranges the Social Security Administration
// never issues: area 000, 666, or 900 and above, group 00, serial 0000.
func validSSN(candidate string) bool {
	area, group, serial := candidate[:3], candidate[4:6], candidate[7:]
	if area == "000" || area == "666" || area[0] == '9' {
		return false
	}
	if group == "00" || serial == "0000" {
		return false
	}
	return true
}
