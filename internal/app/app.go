package app

import (
	"fmt"
	"io"
	"strings"

	"github.com/amxv/procoder/internal/apply"
	"github.com/amxv/procoder/internal/errs"
	"github.com/amxv/procoder/internal/prepare"
)

const commandName = "procoder"

var version = "dev"
var runPrepare = prepare.Run
var runApplyDryRun = apply.RunDryRun
var runApply = apply.Run

func Run(args []string, stdout, stderr io.Writer) error {
	_ = stderr

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

		parsed, err := parseApplyArgs(args[1:])
		if err != nil {
			return err
		}

		if parsed.DryRun {
			plan, err := runApplyDryRun(apply.Options{
				ReturnPackagePath: parsed.ReturnPackagePath,
				Namespace:         parsed.Namespace,
			})
			if err != nil {
				return err
			}
			writeLines(stdout, apply.FormatDryRun(plan))
			return nil
		}

		result, err := runApply(apply.Options{
			ReturnPackagePath: parsed.ReturnPackagePath,
			Namespace:         parsed.Namespace,
			Checkout:          parsed.Checkout,
		})
		if err != nil {
			return err
		}
		writeLines(stdout, apply.FormatSuccess(result))
		return nil
	default:
		return errs.New(
			errs.CodeUnknownCommand,
			fmt.Sprintf("unknown command %q", args[0]),
			errs.WithHint(fmt.Sprintf("run `%s --help`", commandName)),
		)
	}
}

type applyArgs struct {
	ReturnPackagePath string
	DryRun            bool
	Namespace         string
	Checkout          bool
}

func parseApplyArgs(args []string) (applyArgs, error) {
	parsed := applyArgs{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--dry-run":
			parsed.DryRun = true
		case arg == "--checkout":
			parsed.Checkout = true
		case arg == "--namespace":
			if i+1 >= len(args) {
				return applyArgs{}, errs.New(
					errs.CodeUnknownCommand,
					"missing value for `--namespace`",
					errs.WithHint("run `procoder apply --help`"),
				)
			}
			i++
			value := strings.TrimSpace(args[i])
			if value == "" {
				return applyArgs{}, errs.New(
					errs.CodeUnknownCommand,
					"namespace prefix for `--namespace` must not be empty",
					errs.WithHint("run `procoder apply --help`"),
				)
			}
			parsed.Namespace = value
		case strings.HasPrefix(arg, "--namespace="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--namespace="))
			if value == "" {
				return applyArgs{}, errs.New(
					errs.CodeUnknownCommand,
					"namespace prefix for `--namespace` must not be empty",
					errs.WithHint("run `procoder apply --help`"),
				)
			}
			parsed.Namespace = value
		case strings.HasPrefix(arg, "-"):
			return applyArgs{}, unknownApplyArgument(arg)
		default:
			if parsed.ReturnPackagePath == "" {
				parsed.ReturnPackagePath = arg
				continue
			}
			return applyArgs{}, unknownApplyArgument(arg)
		}
	}

	if strings.TrimSpace(parsed.ReturnPackagePath) == "" {
		return applyArgs{}, errs.New(
			errs.CodeUnknownCommand,
			"missing return package path for `procoder apply`",
			errs.WithHint("run `procoder apply --help`"),
		)
	}

	return parsed, nil
}

func unknownApplyArgument(arg string) error {
	return errs.New(
		errs.CodeUnknownCommand,
		fmt.Sprintf("unknown argument %q for `procoder apply`", arg),
		errs.WithHint("run `procoder apply --help`"),
	)
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
		"  prepare                        create a task package",
		"  apply <return-package.zip>     apply a return package",
		"  version                        print CLI version",
		"",
		"Examples:",
		"  procoder prepare",
		"  procoder apply procoder-return-<exchange-id>.zip --dry-run",
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
		"  procoder apply procoder-return-<exchange-id>.zip --dry-run --namespace procoder-import",
	)
}

func writeLines(w io.Writer, lines ...string) {
	for _, line := range lines {
		_, _ = fmt.Fprintln(w, line)
	}
}
