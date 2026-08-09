// Package main is the Starport process boundary.
package main

import "os"

func main() {
	os.Exit(run(os.Args, os.Stdin, os.Stdout, os.Stderr))
}
