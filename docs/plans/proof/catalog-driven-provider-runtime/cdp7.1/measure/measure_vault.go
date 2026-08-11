//go:build measure_vault

package main

import (
	"context"
	"os"

	vault "github.com/hashicorp/vault/api"
)

func init() {
	if os.Getenv("STARPORT_MEASURE_VAULT") == "" {
		return
	}
	client, _ := vault.NewClient(vault.DefaultConfig())
	if client != nil {
		_, _ = client.KVv2("secret").Get(context.Background(), "example")
	}
}
