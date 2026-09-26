package catalog

import "testing"

func TestAcquisitionRoleSelectionContract(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   string
		refuse bool
	}{
		{name: "catalog role wins", values: map[string]string{"STARPORT_CATALOG_TESTPROVIDER_API_KEY": "catalog", "STARMAP_TESTPROVIDER_API_KEY": "map", "STARPORT_TESTPROVIDER_API_KEY": "gateway", "TESTPROVIDER_API_KEY": "conventional"}, want: "catalog"},
		{name: "starmap precedes gateway", values: map[string]string{"STARMAP_TESTPROVIDER_API_KEY": "map", "STARPORT_TESTPROVIDER_API_KEY": "gateway"}, want: "map"},
		{name: "empty role disables fallback", values: map[string]string{"STARPORT_CATALOG_TESTPROVIDER_API_KEY": "", "TESTPROVIDER_API_KEY": "conventional"}, refuse: true},
		{name: "empty gateway disables fallback", values: map[string]string{"STARPORT_TESTPROVIDER_API_KEY": "", "TESTPROVIDER_API_KEY": "conventional"}, refuse: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewAcquisitionResolver(func(name string) (string, bool) { v, ok := tt.values[name]; return v, ok })
			material, err := resolver.ResolveCatalog(t.Context(), acquisitionTestProvider(true))
			if tt.refuse {
				if err == nil {
					t.Fatal("explicit empty selection permitted credential fallback")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve catalog: %v", err)
			}
			value, ok := material.Value("api-key")
			if !ok || value != tt.want {
				t.Fatal("selected credential does not follow acquisition role precedence")
			}
		})
	}
}
