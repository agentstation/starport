//go:build openbao || all

package main

import (
	"context"

	openbao "github.com/openbao/openbao/api/v2"
)

func init() {
	reads = append(reads, func(ctx context.Context) error {
		client, err := openbao.NewClient(openbao.DefaultConfig())
		if err != nil {
			return err
		}
		_, err = client.KVv2("secret").Get(ctx, "example")
		return err
	})
}
