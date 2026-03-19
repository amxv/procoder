package gitx

import (
	"bytes"
	stderrors "errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/amxv/procoder/internal/errs"
)

type Runner struct {
	Dir string
	Env []string
}

type Result struct {
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
}

func NewRunner(dir string) Runner {
	return Runner{Dir: dir}
}

func (r Runner) Run(args ...string) (Result, error) {
	return r.run(nil, args...)
}

func (r Runner) RunWithInput(input string, args ...string) (Result, error) {
	return r.run(strings.NewReader(input), args...)
}

func (r Runner) run(stdin io.Reader, args ...string) (Result, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	if len(r.Env) > 0 {
		cmd.Env = append(cmd.Environ(), r.Env...)
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	result := Result{
		Args: args,
	}

	err := cmd.Run()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	result.ExitCode = getExitCode(err)
	if err == nil {
		return result, nil
	}

	var execErr *exec.Error
	if stderrors.As(err, &execErr) {
		return result, errs.Wrap(
			errs.CodeGitUnavailable,
			"git executable is unavailable",
			err,
			errs.WithHint("install Git and retry"),
		)
	}

	commandText := "git"
	if len(args) > 0 {
		commandText += " " + strings.Join(args, " ")
	}
	details := []string{
		"Command: " + commandText,
		"Exit code: " + strconv.Itoa(result.ExitCode),
	}
	stderrText := strings.TrimRight(result.Stderr, "\n")
	if stderrText != "" {
		details = append(details, "Stderr: "+stderrText)
	}
	return result, errs.Wrap(
		errs.CodeGitCommandFailed,
		fmt.Sprintf("git command failed: %s", commandText),
		err,
		errs.WithDetails(details...),
	)
}

func getExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if stderrors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
