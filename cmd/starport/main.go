// Package main is the entry point for the Starport application.
package main

import "log"

func main() {
	if err := run(); err != nil {
		log.Fatalf("Fatal error: %v", err)
	}
}
