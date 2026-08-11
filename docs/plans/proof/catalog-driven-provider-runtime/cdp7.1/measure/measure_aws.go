//go:build measure_aws

package main

import (
	"context"
	"os"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

func init() {
	if os.Getenv("STARPORT_MEASURE_AWS") == "" {
		return
	}
	ctx := context.Background()
	config, _ := awsconfig.LoadDefaultConfig(ctx)
	client := secretsmanager.NewFromConfig(config)
	secretID := "example"
	_, _ = client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: &secretID})
}
