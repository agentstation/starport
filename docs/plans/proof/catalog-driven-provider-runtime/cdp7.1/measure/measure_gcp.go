//go:build measure_gcp

package main

import (
	"context"
	"os"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
)

func init() {
	if os.Getenv("STARPORT_MEASURE_GCP") == "" {
		return
	}
	ctx := context.Background()
	client, _ := secretmanager.NewClient(ctx)
	if client == nil {
		return
	}
	_, _ = client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: "projects/example/secrets/example/versions/latest",
	})
	_ = client.Close()
}
