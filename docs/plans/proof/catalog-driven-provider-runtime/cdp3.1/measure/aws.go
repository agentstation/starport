//go:build aws || all

package main

import (
	"context"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

func init() {
	reads = append(reads, func(ctx context.Context) error {
		config, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return err
		}
		client := secretsmanager.NewFromConfig(config)
		secretID := "example"
		_, err = client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
			SecretId: &secretID,
		})
		return err
	})
}
