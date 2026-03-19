package gitrepo

import (
	"bytes"
	stderrors "errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type Repo struct {
	t   *testing.T
	Dir string
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func New(t *testing.T) *Repo {
	t.Helper()

	dir := t.TempDir()
	r := &Repo{
		t:   t,
		Dir: dir,
	}

	result := r.Run("init", "--initial-branch=main")
	if result.ExitCode != 0 {
		result = r.Run("init")
		if result.ExitCode != 0 {
			t.Fatalf("git init failed:\nstdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
		}
		r.Git("branch", "-M", "main")
	}

	r.Git("config", "user.name", "Test User")
	r.Git("config", "user.email", "test@example.com")
	r.Git("config", "commit.gpgsign", "false")

	return r
}

func (r *Repo) WriteFile(relPath, contents string) string {
	r.t.Helper()

	fullPath := filepath.Join(r.Dir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		r.t.Fatalf("mkdir for %s failed: %v", relPath, err)
	}
	if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
		r.t.Fatalf("write %s failed: %v", relPath, err)
	}
	return fullPath
}

func (r *Repo) CommitAll(message string) string {
	r.t.Helper()

	r.Git("add", "-A")
	r.Git("commit", "-m", message)
	return strings.TrimSpace(r.Git("rev-parse", "HEAD"))
}

func (r *Repo) Git(args ...string) string {
	r.t.Helper()

	result := r.Run(args...)
	if result.ExitCode != 0 {
		r.t.Fatalf(
			"git %s failed with code %d\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "),
			result.ExitCode,
			result.Stdout,
			result.Stderr,
		)
	}
	return result.Stdout
}

func (r *Repo) Run(args ...string) Result {
	r.t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode(err),
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if stderrors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
