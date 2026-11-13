package main

import (
	"fmt"
	"os"

	"llm-memory/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "llmmem:", err)
		os.Exit(1)
	}
}
