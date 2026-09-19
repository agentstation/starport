package credentials

import "testing"

func TestResolvedDestinationHandleSurvivesResolverRestart(t *testing.T) {
	provider := staticCredentialProvider()
	resolve := func(secret string) Material {
		t.Helper()
		handle := referenceHandle(t, provider, NewResolver(WithEnvironmentLookup(mapLookup(map[string]string{"EXPLICIT_KEY": secret}))), "env:EXPLICIT_KEY", false)
		material, err := handle.ResolveMaterial(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return material
	}
	first := resolve("valid-first")
	second := resolve("valid-second")
	if first.Handle() == "" || first.Handle() != second.Handle() {
		t.Fatal("source identity changed across resolver restart and rotation")
	}
	provider.ID = "another-provider"
	third := resolve("valid-first")
	if third.Handle() == first.Handle() {
		t.Fatal("provider identity did not separate handles")
	}
	if first.Version() == second.Version() {
		t.Fatal("material rotation retained its version")
	}
}
