package main

import (
	"os"

	"github.com/nmelo/gasadd/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
