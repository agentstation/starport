// Command smoke_openrouter_go verifies the official OpenRouter Go SDK against Starport.
package main

import (
	"context"
	"fmt"
	"os"

	openrouter "github.com/OpenRouterTeam/go-sdk"
	"github.com/OpenRouterTeam/go-sdk/models/components"
)

func main() {
	client := openrouter.New(
		openrouter.WithSecurity(os.Getenv("STARPORT_SMOKE_API_KEY")),
		openrouter.WithServerURL(os.Getenv("STARPORT_SMOKE_BASE_URL")),
	)
	response, err := client.Chat.Send(context.Background(), components.ChatRequest{
		Model: openrouter.Pointer("openai/gpt-4.1"),
		Messages: []components.ChatMessages{
			components.CreateChatMessagesUser(components.ChatUserMessage{
				Role: components.ChatUserMessageRoleUser,
				Content: components.CreateChatUserMessageContentStr(
					"smoke",
				),
			}),
		},
	}, nil)
	if err != nil {
		panic(err)
	}
	if response == nil || response.ChatResult == nil || len(response.ChatResult.Choices) != 1 {
		panic("unexpected OpenRouter Go SDK response shape")
	}
	content, ok := response.ChatResult.Choices[0].Message.Content.GetOrZero()
	if !ok || content.Str == nil || *content.Str != "starport smoke ok" {
		panic("unexpected OpenRouter Go SDK response content")
	}
	fmt.Println("PASS Go OpenRouter SDK")
}
