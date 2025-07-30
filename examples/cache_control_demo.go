// Package main demonstrates how to use cache control with Starport
package main

import (
	"encoding/json"
	"fmt"
)

func main() {
	// Example request with cache control for Anthropic
	request := map[string]interface{}{
		"model": "anthropic/claude-3-5-sonnet",
		"messages": []interface{}{
			map[string]interface{}{
				"role": "system",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "You are a helpful assistant. Here is some context that should be cached for future requests...",
						"cache_control": map[string]string{
							"type": "ephemeral",
						},
					},
				},
			},
			map[string]interface{}{
				"role":    "user",
				"content": "What is the capital of France?",
			},
		},
		"max_tokens": 100,
	}

	// Marshal request to JSON
	jsonData, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling request: %v\n", err)
		return
	}

	fmt.Println("Example cache control request:")
	fmt.Println(string(jsonData))
	fmt.Println("\nThis request would:")
	fmt.Println("1. Cache the system message for future requests")
	fmt.Println("2. Save on prompt tokens for subsequent requests")
	fmt.Println("3. Include X-Cache headers in the response")
	fmt.Println("4. Show cache pricing in X-Cache-Write-Cost and X-Cache-Read-Cost headers")

	// To make the actual request, you would use:
	// resp, err := http.Post("http://localhost:8080/v1/chat/completions",
	//     "application/json", bytes.NewBuffer(jsonData))
	//
	// Then check the cache headers:
	// X-Cache: Cache status (HIT/MISS)
	// X-Cache-Write-Cost: Cost of writing to cache
	// X-Cache-Read-Cost: Cost of reading from cache
}
