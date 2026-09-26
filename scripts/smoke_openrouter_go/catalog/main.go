package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	openrouter "github.com/OpenRouterTeam/go-sdk"
	"github.com/OpenRouterTeam/go-sdk/models/components"
	"github.com/OpenRouterTeam/go-sdk/models/sdkerrors"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := openrouter.New(openrouter.WithSecurity(os.Getenv("STARPORT_CATALOG_KEY")), openrouter.WithServerURL(os.Getenv("STARPORT_CATALOG_URL")+"/api/v1"))
	for _, model := range []string{"author/current", "author/old"} {
		for _, streaming := range []bool{false, true} {
			removed := os.Args[1] == "removed" || os.Args[1] == "alias-removed" && model == "author/old"
			response, err := client.Chat.Send(ctx, components.ChatRequest{Model: openrouter.Pointer(model), Stream: openrouter.Pointer(streaming), Messages: []components.ChatMessages{components.CreateChatMessagesUser(components.ChatUserMessage{Role: components.ChatUserMessageRoleUser, Content: components.CreateChatUserMessageContentStr("hello")})}}, nil)
			if removed {
				if _, ok := errors.AsType[*sdkerrors.NotFoundResponseError](err); !ok {
					panic(fmt.Sprintf("expected SDK not-found error, got %v", err))
				}
				continue
			}
			if err != nil {
				panic(err)
			}
			if streaming {
				if response == nil || response.EventStream == nil {
					panic("missing stream")
				}
				count := 0
				for response.EventStream.Next() {
					count++
				}
				err = response.EventStream.Err()
				closeErr := response.EventStream.Close()
				if err != nil {
					panic(err)
				}
				if closeErr != nil {
					panic(closeErr)
				}
				if count == 0 {
					panic("empty stream")
				}
			} else {
				if response == nil || response.ChatResult == nil || len(response.ChatResult.Choices) != 1 {
					panic("invalid completion")
				}
				content, ok := response.ChatResult.Choices[0].Message.Content.GetOrZero()
				if !ok || content.Str == nil || !strings.Contains(*content.Str, "mock") {
					panic("unexpected completion")
				}
			}
		}
	}
	fmt.Println("PASS Go SDK catalog transition: " + os.Args[1])
}
