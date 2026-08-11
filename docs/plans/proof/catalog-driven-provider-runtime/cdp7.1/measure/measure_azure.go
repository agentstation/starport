//go:build measure_azure

package main

import (
	"context"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

func init() {
	if os.Getenv("STARPORT_MEASURE_AZURE") == "" {
		return
	}
	credential, _ := azidentity.NewDefaultAzureCredential(nil)
	client, _ := azsecrets.NewClient("https://example.vault.azure.net", credential, nil)
	if client != nil {
		_, _ = client.GetSecret(context.Background(), "example", "", nil)
	}
}
