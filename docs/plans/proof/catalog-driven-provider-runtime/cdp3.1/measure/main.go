package main

import (
	"context"
	"fmt"
	"net/http"

	"cloud.google.com/go/auth/credentials"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var reads []func(context.Context) error

func main() {
	ctx := context.Background()
	_, _ = credentials.DetectDefault(&credentials.DetectOptions{})
	_, _ = azidentity.NewDefaultAzureCredential(nil)
	_, _ = awsconfig.LoadDefaultConfig(ctx)
	connection, _ := grpc.NewClient(
		"passthrough:///localhost",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if connection != nil {
		_ = connection.Close()
	}
	_ = http.DefaultClient
	for _, read := range reads {
		_ = read(ctx)
	}
	fmt.Print(len(reads))
}
