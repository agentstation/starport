package router

import (
	"context"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

func (r *mockRegistry) ResolveMaterial(context.Context, string) (credentials.Material, error) {
	return routerTestMaterial(), nil
}

func (r *mockConnectorRegistry) ResolveMaterial(
	context.Context,
	string,
) (credentials.Material, error) {
	return routerTestMaterial(), nil
}

func routerTestMaterial() credentials.Material {
	return credentials.NewMaterial(
		catalogs.ProviderCredentialProfile{ID: "none", Primitive: catalogs.ProviderAuthenticationNone},
		nil,
		credentials.MaterialMetadata{Version: "test"},
	)
}
