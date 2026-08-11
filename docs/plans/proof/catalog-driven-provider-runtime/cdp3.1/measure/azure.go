//go:build azure || all

package main

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

func init() {
	reads = append(reads, func(ctx context.Context) error {
		credential, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return err
		}
		client, err := azsecrets.NewClient("https://example.vault.azure.net", credential, nil)
		if err != nil {
			return err
		}
		_, err = client.GetSecret(ctx, "example", "", nil)
		return err
	})
}
