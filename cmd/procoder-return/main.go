package main

import (
	"io"
	"os"

	"github.com/amxv/procoder/internal/errs"
	"github.com/amxv/procoder/internal/output"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && isHelpArg(args[0]) {
		printHelp(os.Stdout)
		return
	}

	output.WriteError(os.Stderr, errs.New(
		errs.CodeNotImplemented,
		"`procoder-return` is not implemented yet",
		errs.WithHint("this command will be wired in a later implementation phase"),
	))
	os.Exit(1)
}

func printHelp(w io.Writer) {
	lines := []string{
		"procoder-return - create a return package",
		"",
		"Usage:",
		"  procoder-return",
		"",
		"This command creates a return package inside a prepared task package.",
	}
	for _, line := range lines {
		_, _ = io.WriteString(w, line+"\n")
	}
}

func isHelpArg(v string) bool {
	switch v {
	case "-h", "--help", "help":
		return true
	default:
		return false
	}
}
