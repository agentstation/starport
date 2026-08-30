package presets

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func validPreset() Preset {
	now := time.Now().UTC()
	return Preset{
		Name:      "fast",
		Config:    Config{Model: "openai/gpt-4o-mini"},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestValidateProviderRoutingPolicy(t *testing.T) {
	preset := validPreset()
	preset.Config.Provider = &ProviderPreferences{
		Sort:                    SortPrice,
		MaxPromptPricePer1M:     2.5,
		MaxCompletionPricePer1M: 10,
	}
	require.NoError(t, preset.Validate())

	for _, sort := range []string{"", SortPrice, SortLatency, SortThroughput, SortSpread} {
		preset.Config.Provider.Sort = sort
		require.NoError(t, preset.Validate(), "sort %q is valid", sort)
	}

	preset.Config.Provider.Sort = "vibes"
	err := preset.Validate()
	require.ErrorIs(t, err, ErrInvalidPreset)
	require.Contains(t, err.Error(), "sort")

	preset.Config.Provider.Sort = ""
	preset.Config.Provider.MaxPromptPricePer1M = -1
	err = preset.Validate()
	require.ErrorIs(t, err, ErrInvalidPreset)
	require.Contains(t, err.Error(), "price caps")
}

func TestCloneCopiesProviderRoutingPolicy(t *testing.T) {
	allow := false
	config := Config{
		Model: "openai/gpt-4o-mini",
		Provider: &ProviderPreferences{
			Order:                   []string{"groq"},
			AllowFallbacks:          &allow,
			Sort:                    SortLatency,
			MaxPromptPricePer1M:     1,
			MaxCompletionPricePer1M: 4,
		},
	}
	clone := config.Clone()
	require.Equal(t, config, clone)

	clone.Provider.Sort = SortPrice
	clone.Provider.MaxPromptPricePer1M = 9
	clone.Provider.Order[0] = "openai"
	require.Equal(t, SortLatency, config.Provider.Sort, "clone must not share provider state")
	require.InDelta(t, 1.0, config.Provider.MaxPromptPricePer1M, 1e-9)
	require.Equal(t, "groq", config.Provider.Order[0])
}
