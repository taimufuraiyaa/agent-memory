package main

import (
	"fmt"
	"os"

	"github.com/taimufuraiyaa/agent-memory/internal/cli"
)

func main() {
	cmd := cli.NewRootCommand()
	cmd.Use = "am"
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
