package credentials

import "testing"

func TestInferenceProductPrecedence(t *testing.T) {
	resolver := NewResolver(WithEnvironmentLookup(mapLookup(map[string]string{"STARPORT_OPENAI_API_KEY": "valid-product", "OPENAI_API_KEY": "valid-conventional"})))
	handle, err := resolver.Provider(staticCredentialProvider(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	material, configured, err := handle.Resolve(t.Context())
	if err != nil || !configured {
		t.Fatalf("resolve inference: %v", err)
	}
	if value, _ := material.Value("api-key"); value != "valid-product" {
		t.Fatal("inference did not select the product credential first")
	}
}

func TestInferenceStarmapFallbackRequiresOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		r := NewResolver(WithEnvironmentLookup(mapLookup(map[string]string{"STARMAP_OPENAI_API_KEY": "valid-catalog"})), WithStarmapFallback(enabled))
		h, err := r.Provider(staticCredentialProvider(), nil, false)
		if err != nil {
			t.Fatal(err)
		}
		material, configured, err := h.Resolve(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if configured != enabled {
			t.Fatal("Starmap inference fallback did not honor opt-in")
		}
		if enabled {
			if v, _ := material.Value("api-key"); v != "valid-catalog" {
				t.Fatal("wrong fallback material")
			}
		}
	}
}

func TestInferenceExplicitEmptyStopsFallback(t *testing.T) {
	r := NewResolver(WithEnvironmentLookup(mapLookup(map[string]string{"STARPORT_OPENAI_API_KEY": "", "OPENAI_API_KEY": "valid-conventional", "STARMAP_OPENAI_API_KEY": "valid-catalog"})), WithStarmapFallback(true))
	h, err := r.Provider(staticCredentialProvider(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = h.Resolve(t.Context()); err == nil {
		t.Fatal("explicit empty inference selection used fallback")
	}
}
