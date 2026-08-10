package main

import (
	"fmt"
	"os"

	"github.com/agentstation/starport/internal/doclinks"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "documentation link verification requires at least one file")
		os.Exit(2)
	}

	broken, err := doclinks.CheckFiles(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "documentation link verification failed: %v\n", err)
		os.Exit(2)
	}
	for _, link := range broken {
		fmt.Printf("FAIL %s:%d missing link target %s\n", link.Source, link.Line, link.Target)
	}
	if len(broken) > 0 {
		fmt.Printf("Summary: %d broken documentation link(s)\n", len(broken))
		os.Exit(1)
	}
	fmt.Println("PASS documentation links")
}
