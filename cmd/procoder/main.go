package main

import (
	"os"

	"github.com/amxv/procoder/internal/app"
	"github.com/amxv/procoder/internal/output"
)

func main() {
	if err := app.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		output.WriteError(os.Stderr, err)
		os.Exit(1)
	}
}
