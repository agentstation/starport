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

func TestGuardrailsConfigCategoryThresholds(t *testing.T) {
	var unset *GuardrailsConfig
	thresholds, err := unset.CategoryThresholds()
	require.NoError(t, err)
	require.Nil(t, thresholds, "a nil config overrides nothing")

	configured := &GuardrailsConfig{ModerationThresholds: " violence=0.8 , self-harm = 0.2 "}
	thresholds, err = configured.CategoryThresholds()
	require.NoError(t, err)
	require.Equal(t, map[string]float64{"violence": 0.8, "self-harm": 0.2}, thresholds)

	for _, malformed := range []string{"violence", "=0.5", "violence=high"} {
		_, err := (&GuardrailsConfig{ModerationThresholds: malformed}).CategoryThresholds()
		require.Error(t, err, "%q must refuse at startup", malformed)
	}
}
