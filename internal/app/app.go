package app

import (
	"fmt"
	"io"

	"github.com/amxv/procoder/internal/errs"
	"github.com/amxv/procoder/internal/prepare"
)

const commandName = "procoder"

var version = "dev"
var runPrepare = prepare.Run

func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printRootHelp(stdout)
		return nil
	}

	switch args[0] {
	case "version":
		_, _ = fmt.Fprintf(stdout, "%s %s\n", commandName, version)
		return nil
	case "prepare":
		if len(args) > 1 && isHelpArg(args[1]) {
			printPrepareHelp(stdout)
			return nil
		}
		if len(args) > 1 {
			return errs.New(
				errs.CodeUnknownCommand,
				fmt.Sprintf("unknown argument %q for `procoder prepare`", args[1]),
				errs.WithHint("run `procoder prepare --help`"),
			)
		}
		result, err := runPrepare(prepare.Options{ToolVersion: version})
		if err != nil {
			return err
		}
		writeLines(stdout, prepare.FormatSuccess(result))
		return nil
	case "apply":
		if len(args) > 1 && isHelpArg(args[1]) {
			printApplyHelp(stdout)
			return nil
		}
		return errs.New(
			errs.CodeNotImplemented,
			"`procoder apply` is not implemented yet",
			errs.WithHint("this command will be wired in a later implementation phase"),
		)
	default:
		return errs.New(
			errs.CodeUnknownCommand,
			fmt.Sprintf("unknown command %q", args[0]),
			errs.WithHint(fmt.Sprintf("run `%s --help`", commandName)),
		)
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

func printRootHelp(w io.Writer) {
	writeLines(w,
		"procoder",
		"",
		"Usage:",
		"  procoder <command> [arguments]",
		"",
		"Commands:",
		"  prepare                       create a task package",
		"  apply <return-package.zip>     apply a return package (coming soon)",
		"  version         print CLI version",
		"",
		"Examples:",
		"  procoder prepare",
		"  procoder apply procoder-return-<exchange-id>.zip",
		"  procoder version",
	)
}

func printPrepareHelp(w io.Writer) {
	writeLines(w,
		"procoder prepare - create a task package",
		"",
		"Usage:",
		"  procoder prepare",
		"",
		"Examples:",
		"  procoder prepare",
	)
}

func printApplyHelp(w io.Writer) {
	writeLines(w,
		"procoder apply - apply a return package",
		"",
		"Usage:",
		"  procoder apply <return-package.zip> [--dry-run] [--namespace <prefix>] [--checkout]",
		"",
		"Examples:",
		"  procoder apply procoder-return-<exchange-id>.zip",
		"  procoder apply procoder-return-<exchange-id>.zip --dry-run",
	)
}

func writeLines(w io.Writer, lines ...string) {
	for _, line := range lines {
		_, _ = fmt.Fprintln(w, line)
	}
}
