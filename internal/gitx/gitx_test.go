package gitx

import (
	"strings"
	"testing"

	"github.com/amxv/procoder/internal/errs"
	"github.com/amxv/procoder/internal/testutil/gitrepo"
)

func TestRunnerRunSuccess(t *testing.T) {
	repo := gitrepo.New(t)
	repo.WriteFile("README.md", "hello\n")
	headOID := repo.CommitAll("initial commit")

	runner := NewRunner(repo.Dir)
	result, err := runner.Run("rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if strings.TrimSpace(result.Stdout) != headOID {
		t.Fatalf("unexpected stdout: got %q want %q", strings.TrimSpace(result.Stdout), headOID)
	}
}

func TestRunnerRunFailure(t *testing.T) {
	repo := gitrepo.New(t)
	runner := NewRunner(repo.Dir)

	result, err := runner.Run("rev-parse", "refs/heads/does-not-exist")
	if err == nil {
		t.Fatal("expected error")
	}
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit code, got %d", result.ExitCode)
	}

	typed, ok := errs.As(err)
	if !ok {
		t.Fatalf("expected typed error, got %T", err)
	}
	if typed.Code != errs.CodeGitCommandFailed {
		t.Fatalf("expected code %s, got %s", errs.CodeGitCommandFailed, typed.Code)
	}
	if !strings.Contains(strings.Join(typed.Details, "\n"), "Exit code:") {
		t.Fatalf("expected exit code detail, got %v", typed.Details)
	}
}
