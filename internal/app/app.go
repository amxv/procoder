package app

import (
	"fmt"
	"io"
	"strings"
)

const commandName = "procoder"

var version = "dev"

func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printRootHelp(stdout)
		return nil
	}

	switch args[0] {
	case "version":
		_, _ = fmt.Fprintf(stdout, "%s %s\n", commandName, version)
		return nil
	case "hello":
		if len(args) > 1 && isHelpArg(args[1]) {
			printHelloHelp(stdout)
			return nil
		}
		name := "world"
		if len(args) > 1 {
			name = strings.TrimSpace(args[1])
			if name == "" {
				name = "world"
			}
		}
		_, _ = fmt.Fprintf(stdout, "Hello, %s!\n", name)
		return nil
	default:
		return fmt.Errorf("unknown command %q (run `%s --help`)", args[0], commandName)
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
		"procoder - Go CLI template",
		"",
		"Usage:",
		"  procoder <command> [arguments]",
		"",
		"Commands:",
		"  hello [name]    print a greeting",
		"  version         print CLI version",
		"",
		"Examples:",
		"  procoder hello",
		"  procoder hello agent",
		"  procoder version",
	)
}

func printHelloHelp(w io.Writer) {
	writeLines(w,
		"procoder hello - print a greeting",
		"",
		"Usage:",
		"  procoder hello [name]",
		"",
		"Examples:",
		"  procoder hello",
		"  procoder hello Alice",
	)
}

func writeLines(w io.Writer, lines ...string) {
	for _, line := range lines {
		_, _ = fmt.Fprintln(w, line)
	}
}
