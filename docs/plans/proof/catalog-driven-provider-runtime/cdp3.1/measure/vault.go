//go:build vault || all

package main

import (
	"context"

	vault "github.com/hashicorp/vault/api"
)

func init() {
	reads = append(reads, func(ctx context.Context) error {
		client, err := vault.NewClient(vault.DefaultConfig())
		if err != nil {
			return err
		}
		_, err = client.KVv2("secret").Get(ctx, "example")
		return err
	})
}
