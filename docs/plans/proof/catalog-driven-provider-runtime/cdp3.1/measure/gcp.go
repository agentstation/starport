//go:build gcp || all

package main

import (
	"context"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
)

func init() {
	reads = append(reads, func(ctx context.Context) error {
		client, err := secretmanager.NewClient(ctx)
		if err != nil {
			return err
		}
		defer client.Close()
		_, err = client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
			Name: "projects/example/secrets/example/versions/latest",
		})
		return err
	})
}
