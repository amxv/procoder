package app_test

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apppkg "github.com/amxv/procoder/internal/app"
	"github.com/amxv/procoder/internal/exchange"
	"github.com/amxv/procoder/internal/returnpkg"
	"github.com/amxv/procoder/internal/testutil/gitrepo"
)

func TestCLIEndToEndPrepareReturnApply(t *testing.T) {
	source := gitrepo.New(t)
	source.WriteFile("README.md", "source\n")
	source.CommitAll("initial")

	helperPath := writeHelperBinary(t)
	t.Setenv("PROCODER_RETURN_HELPER", helperPath)
	t.Chdir(source.Dir)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := apppkg.Run([]string{"prepare"}, &stdout, &stderr); err != nil {
		t.Fatalf("app.Run prepare failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "Prepared exchange.") {
		t.Fatalf("expected prepare success output, got:\n%s", stdout.String())
	}

	taskPackages, err := filepath.Glob(filepath.Join(source.Dir, "procoder-task-*.zip"))
	if err != nil {
		t.Fatalf("glob task package failed: %v", err)
	}
	if len(taskPackages) != 1 {
		t.Fatalf("expected exactly one task package, got %d (%v)", len(taskPackages), taskPackages)
	}

	exportRoot := unzipTaskPackage(t, taskPackages[0])
	ex, err := exchange.ReadExchange(filepath.Join(exportRoot, ".git", "procoder", "exchange.json"))
	if err != nil {
		t.Fatalf("ReadExchange(export) failed: %v", err)
	}

	appendFile(t, filepath.Join(exportRoot, "README.md"), "remote change\n")
	runGit(t, exportRoot, "add", "README.md")
	runGit(t, exportRoot, "commit", "-m", "remote task update")

	retResult, err := returnpkg.Run(returnpkg.Options{
		CWD:         exportRoot,
		ToolVersion: "0.1.0-test",
		Now:         func() time.Time { return time.Date(2026, time.March, 20, 16, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("returnpkg.Run failed: %v", err)
	}

	retRecord := readReturnRecordFromZip(t, retResult.ReturnPackagePath)
	taskUpdate, ok := findUpdate(retRecord.Updates, ex.Task.RootRef)
	if !ok {
		t.Fatalf("expected task root update in return record")
	}

	stdout.Reset()
	stderr.Reset()
	if err := apppkg.Run([]string{"apply", retResult.ReturnPackagePath, "--checkout"}, &stdout, &stderr); err != nil {
		t.Fatalf("app.Run apply failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "Applied return package.") {
		t.Fatalf("expected apply success output, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Checked out: "+ex.Task.RootRef) {
		t.Fatalf("expected checked-out ref in apply output, got:\n%s", stdout.String())
	}

	if got := strings.TrimSpace(source.Git("rev-parse", ex.Task.RootRef)); got != taskUpdate.NewOID {
		t.Fatalf("task ref mismatch after CLI apply: got %q want %q", got, taskUpdate.NewOID)
	}
	if got := strings.TrimSpace(source.Git("symbolic-ref", "--quiet", "HEAD")); got != ex.Task.RootRef {
		t.Fatalf("expected CLI apply to check out task ref: got %q want %q", got, ex.Task.RootRef)
	}
	assertNoImportRefs(t, source.Dir)
}

func writeHelperBinary(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "procoder-return")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write helper binary failed: %v", err)
	}
	return path
}

func readReturnRecordFromZip(t *testing.T, zipPath string) exchange.Return {
	t.Helper()

	extracted := unzipFile(t, zipPath)
	record, err := exchange.ReadReturn(filepath.Join(extracted, "procoder-return.json"))
	if err != nil {
		t.Fatalf("ReadReturn failed: %v", err)
	}
	return record
}

func unzipTaskPackage(t *testing.T, zipPath string) string {
	t.Helper()

	dest := unzipFile(t, zipPath)
	return filepath.Join(dest, filepath.Base(filepath.Dir(zipPath)))
}

func unzipFile(t *testing.T, zipPath string) string {
	t.Helper()

	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("zip.OpenReader failed: %v", err)
	}
	defer reader.Close()

	dest := t.TempDir()
	for _, file := range reader.File {
		targetPath := filepath.Join(dest, file.Name)
		safePrefix := filepath.Clean(dest) + string(os.PathSeparator)
		if !strings.HasPrefix(filepath.Clean(targetPath), safePrefix) {
			t.Fatalf("zip entry escapes destination: %q", file.Name)
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				t.Fatalf("mkdir failed: %v", err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			t.Fatalf("mkdir file dir failed: %v", err)
		}
		in, err := file.Open()
		if err != nil {
			t.Fatalf("open zip entry failed: %v", err)
		}
		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, file.Mode())
		if err != nil {
			_ = in.Close()
			t.Fatalf("open extracted file failed: %v", err)
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			_ = in.Close()
			t.Fatalf("copy extracted file failed: %v", err)
		}
		_ = out.Close()
		_ = in.Close()
	}
	return dest
}

func appendFile(t *testing.T, path, extra string) {
	t.Helper()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s for append failed: %v", path, err)
	}
	if _, err := f.WriteString(extra); err != nil {
		_ = f.Close()
		t.Fatalf("append to %s failed: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s failed: %v", path, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s failed:\nstdout:\n%s\nstderr:\n%s\nerr:%v", strings.Join(args, " "), stdout.String(), stderr.String(), err)
	}
	return stdout.String()
}

func findUpdate(updates []exchange.RefUpdate, ref string) (exchange.RefUpdate, bool) {
	for _, update := range updates {
		if update.Ref == ref {
			return update, true
		}
	}
	return exchange.RefUpdate{}, false
}

func assertNoImportRefs(t *testing.T, dir string) {
	t.Helper()

	importRefs := strings.TrimSpace(runGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/procoder/import"))
	if importRefs != "" {
		t.Fatalf("expected temporary import refs to be cleaned up, got:\n%s", importRefs)
	}
}
