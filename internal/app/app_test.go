package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/amxv/procoder/internal/apply"
	"github.com/amxv/procoder/internal/errs"
	"github.com/amxv/procoder/internal/prepare"
)

func TestRunRootHelp(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{"--help"}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("expected help output, got: %q", out.String())
	}
	if !strings.Contains(out.String(), "prepare") {
		t.Fatalf("expected prepare command in help output, got: %q", out.String())
	}
}

func TestRunVersion(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{"version"}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !strings.Contains(out.String(), "procoder ") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestRunApplyHelpTerminology(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{"apply", "--help"}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "return package") {
		t.Fatalf("expected return package terminology in apply help, got: %q", got)
	}
	if strings.Contains(got, "<reply.zip>") {
		t.Fatalf("unexpected stale reply terminology in apply help, got: %q", got)
	}
}

func TestRunPrepareCommand(t *testing.T) {
	originalRunPrepare := runPrepare
	t.Cleanup(func() {
		runPrepare = originalRunPrepare
	})

	runPrepare = func(opts prepare.Options) (prepare.Result, error) {
		if opts.ToolVersion == "" {
			t.Fatal("expected tool version to be passed into prepare options")
		}
		return prepare.Result{
			ExchangeID:      "20260320-120000-a1b2c3",
			TaskRootRef:     "refs/heads/procoder/20260320-120000-a1b2c3/task",
			TaskPackagePath: "/tmp/procoder-task-20260320-120000-a1b2c3.zip",
		}, nil
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{"prepare"}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Prepared exchange.") {
		t.Fatalf("expected prepare success header, got: %q", got)
	}
	if !strings.Contains(got, "refs/heads/procoder/20260320-120000-a1b2c3/task") {
		t.Fatalf("expected task branch in output, got: %q", got)
	}
	if !strings.Contains(got, "/tmp/procoder-task-20260320-120000-a1b2c3.zip") {
		t.Fatalf("expected task package path in output, got: %q", got)
	}
}

func TestRunApplyDryRunCommand(t *testing.T) {
	originalRunApplyDryRun := runApplyDryRun
	t.Cleanup(func() {
		runApplyDryRun = originalRunApplyDryRun
	})

	runApplyDryRun = func(opts apply.Options) (apply.Plan, error) {
		if opts.ReturnPackagePath != "procoder-return-20260320-120000-a1b2c3.zip" {
			t.Fatalf("unexpected return package path: %q", opts.ReturnPackagePath)
		}
		if opts.Namespace != "procoder-import" {
			t.Fatalf("unexpected namespace: %q", opts.Namespace)
		}
		return apply.Plan{
			ExchangeID:        "20260320-120000-a1b2c3",
			ReturnPackagePath: "/tmp/procoder-return-20260320-120000-a1b2c3.zip",
			Namespace:         "procoder-import",
			Checks: []apply.Check{
				{Name: "bundle verification", Detail: "git bundle verify passed"},
			},
			Entries: []apply.PlanEntry{
				{
					Action:         apply.ActionUpdate,
					SourceRef:      "refs/heads/procoder/20260320-120000-a1b2c3/task",
					DestinationRef: "refs/heads/procoder-import/20260320-120000-a1b2c3/task",
					OldOID:         "old",
					NewOID:         "new",
					Remapped:       true,
				},
			},
			Summary: apply.Summary{Updates: 1},
		}, nil
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := Run([]string{
		"apply",
		"procoder-return-20260320-120000-a1b2c3.zip",
		"--dry-run",
		"--namespace",
		"procoder-import",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Dry-run apply plan.") {
		t.Fatalf("expected dry-run plan header, got: %q", got)
	}
	if !strings.Contains(got, "REMAP") {
		t.Fatalf("expected remap output, got: %q", got)
	}
}

func TestRunApplyWriteModeNotImplementedInPhase4(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{"apply", "procoder-return.zip"}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected not implemented error")
	}

	typed, ok := errs.As(err)
	if !ok {
		t.Fatalf("expected typed error, got %T", err)
	}
	if typed.Code != errs.CodeNotImplemented {
		t.Fatalf("unexpected code: got %s want %s", typed.Code, errs.CodeNotImplemented)
	}
	if !strings.Contains(typed.Hint, "--dry-run") {
		t.Fatalf("expected --dry-run hint, got %q", typed.Hint)
	}
}

func TestRunApplyMissingPackagePath(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{"apply", "--dry-run"}, &out, &errBuf)
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
	if !strings.Contains(typed.Message, "missing return package path") {
		t.Fatalf("expected missing path message, got %q", typed.Message)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{"unknown"}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected error for unknown command")
	}

	typed, ok := errs.As(err)
	if !ok {
		t.Fatalf("expected typed error, got %T", err)
	}
	if typed.Code != errs.CodeUnknownCommand {
		t.Fatalf("unexpected error code: %s", typed.Code)
	}
}
