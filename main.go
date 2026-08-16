package main

import (
	"github.com/dymmer-code/dym/cmd"
	"os"
)

func main() {
	if err := cmd.NewRootCommand(cmd.Dependencies{}).Execute(); err != nil {
		os.Exit(1)
	}
}
