package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/amxv/procoder/internal/errs"
	"github.com/amxv/procoder/internal/returnpkg"
)

func TestPrintHelpTerminology(t *testing.T) {
	var out bytes.Buffer
	printHelp(&out)

	got := out.String()
	if !strings.Contains(got, "return package") {
		t.Fatalf("expected return package terminology, got: %q", got)
	}
	if !strings.Contains(got, "prepared task package") {
		t.Fatalf("expected prepared task package terminology, got: %q", got)
	}
	if strings.Contains(got, "remote handoff flow") {
		t.Fatalf("unexpected stale wording in help output, got: %q", got)
	}
}

func TestRunSuccessOutputIncludesSandboxHint(t *testing.T) {
	originalRunReturn := runReturn
	t.Cleanup(func() {
		runReturn = originalRunReturn
	})

	runReturn = func(opts returnpkg.Options) (returnpkg.Result, error) {
		if opts.ToolVersion == "" {
			t.Fatal("expected tool version in returnpkg options")
		}
		return returnpkg.Result{
			ExchangeID:        "20260320-120000-a1b2c3",
			ReturnPackagePath: "/tmp/procoder-return-20260320-120000-a1b2c3.zip",
		}, nil
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := run(nil, &out, &errBuf); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Created return package.") {
		t.Fatalf("expected success header, got %q", got)
	}
	if !strings.Contains(got, "/tmp/procoder-return-20260320-120000-a1b2c3.zip") {
		t.Fatalf("expected absolute return package path, got %q", got)
	}
	if !strings.Contains(got, "sandbox:/tmp/procoder-return-20260320-120000-a1b2c3.zip") {
		t.Fatalf("expected sandbox hint output, got %q", got)
	}
}

func TestRunUnknownArgument(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := run([]string{"unexpected"}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected error")
	}
	typed, ok := errs.As(err)
	if !ok {
		t.Fatalf("expected typed error, got %T", err)
	}
	if typed.Code != errs.CodeUnknownCommand {
		t.Fatalf("unexpected code: got %s want %s", typed.Code, errs.CodeUnknownCommand)
	}
}
