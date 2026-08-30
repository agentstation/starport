package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGuardrailsConfigNames(t *testing.T) {
	var unset *GuardrailsConfig
	require.Nil(t, unset.Names(), "a nil config keeps guardrails off")
	require.Nil(t, (&GuardrailsConfig{}).Names(), "an empty config keeps guardrails off")
	require.Empty(t, (&GuardrailsConfig{Checks: " , "}).Names(), "separators alone name no check")

	configured := &GuardrailsConfig{Checks: " pii , moderation "}
	require.Equal(t, []string{"pii", "moderation"}, configured.Names(),
		"names keep their configured order")
}
