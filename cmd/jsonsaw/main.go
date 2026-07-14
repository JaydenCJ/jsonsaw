// Command jsonsaw saws giant JSON arrays into JSONL streams with constant
// memory, and welds them back together. See `jsonsaw help`.
package main

import (
	"os"

	"github.com/JaydenCJ/jsonsaw/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
