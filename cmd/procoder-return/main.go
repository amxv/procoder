package main

import (
	"fmt"
	"io"
	"os"

	"github.com/amxv/procoder/internal/errs"
	"github.com/amxv/procoder/internal/output"
	"github.com/amxv/procoder/internal/returnpkg"
)

var version = "dev"
var runReturn = returnpkg.Run

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		output.WriteError(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	_ = stderr

	if len(args) > 0 && isHelpArg(args[0]) {
		printHelp(stdout)
		return nil
	}
	if len(args) > 0 {
		return errs.New(
			errs.CodeUnknownCommand,
			fmt.Sprintf("unknown argument %q for `procoder-return`", args[0]),
			errs.WithHint("run `procoder-return --help`"),
		)
	}

	result, err := runReturn(returnpkg.Options{ToolVersion: version})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, returnpkg.FormatSuccess(result))
	return nil
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
