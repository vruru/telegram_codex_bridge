package main

import (
	"fmt"
	"os"

	"telegram-codex-bridge/internal/control"
)

func main() {
	if err := control.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "bridgectl: %v\n", err)
		os.Exit(1)
	}
}
