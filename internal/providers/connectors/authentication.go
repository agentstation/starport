package connectors

import (
	"fmt"
	"net/http"

	"github.com/agentstation/starport/internal/credentials"
	providerauth "github.com/agentstation/starport/internal/providers/auth"
)

var productionAuthentication, productionAuthenticationErr = providerauth.ProductionRegistry()

func applyRequestAuthentication(material credentials.Material, request *http.Request) error {
	if productionAuthenticationErr != nil {
		return fmt.Errorf("open provider authentication registry: %w", productionAuthenticationErr)
	}
	return productionAuthentication.Apply(material, request)
}
