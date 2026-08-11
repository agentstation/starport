//go:build measure_openbao

package main

import (
	"context"
	"os"

	openbao "github.com/openbao/openbao/api/v2"
)

func init() {
	if os.Getenv("STARMAP_MEASURE_OPENBAO") == "" {
		return
	}
	client, _ := openbao.NewClient(openbao.DefaultConfig())
	if client != nil {
		_, _ = client.KVv2("secret").Get(context.Background(), "example")
	}
}
