package guardrails

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestPIICheck(t *testing.T, mode string) Check {
	t.Helper()
	check, err := newPIICheck(Settings{PIIMode: mode})
	require.NoError(t, err)
	return check
}

// TestPIIDetectsCardNumbersUnderLuhn holds the card contract: a digit
// run redacts only when the Luhn checksum names it a card number.
func TestPIIDetectsCardNumbersUnderLuhn(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{
			name: "a bare card number redacts",
			text: "card 4111111111111111 please",
			want: "card [redacted-card] please",
		},
		{
			name: "a spaced card number redacts",
			text: "pay with 4111 1111 1111 1111 today",
			want: "pay with [redacted-card] today",
		},
		{
			name: "a dashed card number redacts",
			text: "5500-0000-0000-0004",
			want: "[redacted-card]",
		},
		{
			name: "a failing checksum stays",
			text: "order 4111111111111112 shipped",
			want: "order 4111111111111112 shipped",
		},
		{
			name: "a short digit run stays",
			text: "invoice 123456789012 due",
			want: "invoice 123456789012 due",
		},
	}
	check := newTestPIICheck(t, "")
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := check.Inspect(context.Background(), Content{Direction: DirectionRequest, Text: testCase.text})
			require.NoError(t, err)
			if testCase.want == testCase.text {
				require.Equal(t, VerdictAllow, result.Verdict)
				return
			}
			require.Equal(t, VerdictRedact, result.Verdict)
			require.Equal(t, testCase.want, result.Redacted)
		})
	}
}

// TestPIIRedactsEachIdentifier holds detection and redaction across the
// four identifier categories, positive and negative cases together.
func TestPIIRedactsEachIdentifier(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{
			name: "an email address redacts",
			text: "write to ada.lovelace@example.co.uk soon",
			want: "write to [redacted-email] soon",
		},
		{
			name: "a dashed us phone number redacts",
			text: "call 415-555-2671 now",
			want: "call [redacted-phone] now",
		},
		{
			name: "a parenthesized phone number redacts",
			text: "call (415) 555-2671 now",
			want: "call [redacted-phone] now",
		},
		{
			name: "an e164 number redacts",
			text: "reach +14155552671 anytime",
			want: "reach [redacted-phone] anytime",
		},
		{
			name: "a dashed ssn redacts",
			text: "ssn 123-45-6789 on file",
			want: "ssn [redacted-ssn] on file",
		},
		{
			name: "an unissued ssn area stays",
			text: "ssn 000-45-6789 on file",
			want: "ssn 000-45-6789 on file",
		},
		{
			name: "a bare ten digit run stays",
			text: "ticket 4155552671 open",
			want: "ticket 4155552671 open",
		},
		{
			name: "a version string stays",
			text: "running v1.2.3-45 today",
			want: "running v1.2.3-45 today",
		},
		{
			name: "clean text stays",
			text: "the quick brown fox",
			want: "the quick brown fox",
		},
		{
			name: "two identifiers both redact",
			text: "ada@example.com or 415-555-2671",
			want: "[redacted-email] or [redacted-phone]",
		},
	}
	check := newTestPIICheck(t, PIIModeRedact)
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := check.Inspect(context.Background(), Content{Direction: DirectionRequest, Text: testCase.text})
			require.NoError(t, err)
			if testCase.want == testCase.text {
				require.Equal(t, VerdictAllow, result.Verdict)
				return
			}
			require.Equal(t, VerdictRedact, result.Verdict)
			require.Equal(t, testCase.want, result.Redacted)
		})
	}
}

// TestPIIRefusesWhenConfigured holds the refuse mode: a finding stops
// the exchange and the reason names the categories, not the values.
func TestPIIRefusesWhenConfigured(t *testing.T) {
	check := newTestPIICheck(t, PIIModeRefuse)

	result, err := check.Inspect(context.Background(), Content{
		Direction: DirectionRequest,
		Text:      "ada@example.com holds card 4111 1111 1111 1111",
	})
	require.NoError(t, err)
	require.Equal(t, VerdictRefuse, result.Verdict)
	require.Equal(t, "personal identifiers found: card, email", result.Reason)
	require.NotContains(t, result.Reason, "4111")

	clean, err := check.Inspect(context.Background(), Content{Direction: DirectionRequest, Text: "clean text"})
	require.NoError(t, err)
	require.Equal(t, VerdictAllow, clean.Verdict)
}

// TestPIIRejectsAnUnknownMode holds the startup contract: a mode this
// build does not ship refuses to construct.
func TestPIIRejectsAnUnknownMode(t *testing.T) {
	_, err := newPIICheck(Settings{PIIMode: "quarantine"})
	require.ErrorContains(t, err, "unknown pii mode")
}
