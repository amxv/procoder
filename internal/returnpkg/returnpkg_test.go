package returnpkg

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/amxv/procoder/internal/errs"
	"github.com/amxv/procoder/internal/exchange"
	"github.com/amxv/procoder/internal/output"
	"github.com/amxv/procoder/internal/prepare"
	"github.com/amxv/procoder/internal/testutil/gitrepo"
)

func TestRunReturnHappyPath(t *testing.T) {
	env := setupPreparedExportRepo(t)

	baseOID := strings.TrimSpace(runGit(t, env.exportRoot, "rev-parse", env.exchange.Task.RootRef))
	appendFile(t, filepath.Join(env.exportRoot, "README.md"), "remote change\n")
	runGit(t, env.exportRoot, "add", "README.md")
	runGit(t, env.exportRoot, "commit", "-m", "remote task work")
	newOID := strings.TrimSpace(runGit(t, env.exportRoot, "rev-parse", env.exchange.Task.RootRef))

	result, err := Run(Options{
		CWD:         env.exportRoot,
		ToolVersion: "0.1.0-test",
		Now:         func() time.Time { return time.Date(2026, time.March, 20, 13, 45, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	expectedName := fmt.Sprintf("procoder-return-%s.zip", env.exchange.ExchangeID)
	expectedPath := filepath.Join(env.exportRoot, expectedName)
	if filepath.Base(result.ReturnPackagePath) != expectedName {
		t.Fatalf("unexpected return package filename: got %q want %q", filepath.Base(result.ReturnPackagePath), expectedName)
	}
	if !filepath.IsAbs(result.ReturnPackagePath) {
		t.Fatalf("expected absolute return package path, got %q", result.ReturnPackagePath)
	}
	gotInfo, err := os.Stat(result.ReturnPackagePath)
	if err != nil {
		t.Fatalf("expected return package at %q: %v", result.ReturnPackagePath, err)
	}
	wantInfo, err := os.Stat(expectedPath)
	if err != nil {
		t.Fatalf("expected return package at %q: %v", expectedPath, err)
	}
	if !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("return package path mismatch: got %q want %q", result.ReturnPackagePath, expectedPath)
	}

	status := strings.TrimSpace(runGit(t, env.exportRoot, "status", "--porcelain=v1", "--untracked-files=all"))
	if status != "" {
		t.Fatalf("expected exported repo to remain clean, got status:\n%s", status)
	}

	exclude := mustReadFile(t, filepath.Join(env.exportRoot, ".git", "info", "exclude"))
	if !strings.Contains(exclude, expectedName) {
		t.Fatalf("expected export exclude to contain return package %q, got:\n%s", expectedName, exclude)
	}

	entries := listZipEntries(t, result.ReturnPackagePath)
	expectedEntries := []string{returnBundleName, returnJSONName}
	sort.Strings(entries)
	sort.Strings(expectedEntries)
	if strings.Join(entries, ",") != strings.Join(expectedEntries, ",") {
		t.Fatalf("unexpected return package contents: got %v want %v", entries, expectedEntries)
	}

	extractDir := unzipFile(t, result.ReturnPackagePath)
	returnRecordPath := filepath.Join(extractDir, returnJSONName)
	returnRecord, err := exchange.ReadReturn(returnRecordPath)
	if err != nil {
		t.Fatalf("ReadReturn failed: %v", err)
	}

	if returnRecord.Protocol != exchange.ReturnProtocolV1 {
		t.Fatalf("unexpected return protocol: got %q want %q", returnRecord.Protocol, exchange.ReturnProtocolV1)
	}
	if returnRecord.ExchangeID != env.exchange.ExchangeID {
		t.Fatalf("unexpected exchange id: got %q want %q", returnRecord.ExchangeID, env.exchange.ExchangeID)
	}
	if returnRecord.BundleFile != returnBundleName {
		t.Fatalf("unexpected bundle file: got %q want %q", returnRecord.BundleFile, returnBundleName)
	}
	if returnRecord.Task.RootRef != env.exchange.Task.RootRef {
		t.Fatalf("unexpected task root ref: got %q want %q", returnRecord.Task.RootRef, env.exchange.Task.RootRef)
	}
	if returnRecord.Task.BaseOID != env.exchange.Task.BaseOID {
		t.Fatalf("unexpected task base oid: got %q want %q", returnRecord.Task.BaseOID, env.exchange.Task.BaseOID)
	}
	if len(returnRecord.Updates) != 1 {
		t.Fatalf("expected exactly one task ref update, got %d", len(returnRecord.Updates))
	}
	update := returnRecord.Updates[0]
	if update.Ref != env.exchange.Task.RootRef {
		t.Fatalf("unexpected updated ref: got %q want %q", update.Ref, env.exchange.Task.RootRef)
	}
	if update.OldOID != baseOID {
		t.Fatalf("unexpected old oid: got %q want %q", update.OldOID, baseOID)
	}
	if update.NewOID != newOID {
		t.Fatalf("unexpected new oid: got %q want %q", update.NewOID, newOID)
	}

	bundlePath := filepath.Join(extractDir, returnBundleName)
	runGit(t, env.exportRoot, "bundle", "verify", bundlePath)

	success := FormatSuccess(result)
	if !strings.Contains(success, "Return package: "+result.ReturnPackagePath) {
		t.Fatalf("expected absolute return package path in success output, got:\n%s", success)
	}
	if !strings.Contains(success, "sandbox:"+filepath.ToSlash(result.ReturnPackagePath)) {
		t.Fatalf("expected sandbox hint in success output, got:\n%s", success)
	}
}

func TestRunReturnAllowsSiblingTaskFamilyBranch(t *testing.T) {
	env := setupPreparedExportRepo(t)

	taskShort := strings.TrimPrefix(env.exchange.Task.RootRef, "refs/heads/")
	siblingRef := env.exchange.Task.RefPrefix + "/experiment"
	siblingShort := strings.TrimPrefix(siblingRef, "refs/heads/")

	runGit(t, env.exportRoot, "branch", siblingShort, taskShort)
	runGit(t, env.exportRoot, "checkout", siblingShort)
	appendFile(t, filepath.Join(env.exportRoot, "README.md"), "sibling branch change\n")
	runGit(t, env.exportRoot, "add", "README.md")
	runGit(t, env.exportRoot, "commit", "-m", "experiment branch work")
	runGit(t, env.exportRoot, "checkout", taskShort)

	result, err := Run(Options{CWD: env.exportRoot})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	extractDir := unzipFile(t, result.ReturnPackagePath)
	returnRecord, err := exchange.ReadReturn(filepath.Join(extractDir, returnJSONName))
	if err != nil {
		t.Fatalf("ReadReturn failed: %v", err)
	}

	update, ok := findUpdate(returnRecord.Updates, siblingRef)
	if !ok {
		t.Fatalf("expected sibling task-family ref %q in return updates, got %#v", siblingRef, returnRecord.Updates)
	}
	if update.OldOID != "" {
		t.Fatalf("expected new sibling ref old oid to be empty, got %q", update.OldOID)
	}
	if strings.TrimSpace(update.NewOID) == "" {
		t.Fatalf("expected new sibling ref new oid to be present")
	}

	runGit(t, env.exportRoot, "bundle", "verify", filepath.Join(extractDir, returnBundleName))
}

func TestRunReturnFailsDirtyWorktree(t *testing.T) {
	env := setupPreparedExportRepo(t)
	appendFile(t, filepath.Join(env.exportRoot, "README.md"), "dirty\n")

	_, err := Run(Options{CWD: env.exportRoot})
	assertErrorContains(t, err, errs.CodeWorktreeDirty,
		"uncommitted or untracked changes",
		"README.md",
		"rerun ./procoder-return",
	)
}

func TestRunReturnFailsUntrackedChanges(t *testing.T) {
	env := setupPreparedExportRepo(t)
	writeFile(t, filepath.Join(env.exportRoot, "scratch.txt"), "untracked\n")

	_, err := Run(Options{CWD: env.exportRoot})
	assertErrorContains(t, err, errs.CodeWorktreeDirty,
		"?? scratch.txt",
		"rerun ./procoder-return",
	)
}

func TestRunReturnFailsNoNewCommits(t *testing.T) {
	env := setupPreparedExportRepo(t)

	_, err := Run(Options{CWD: env.exportRoot})
	assertErrorContains(t, err, errs.CodeNoNewCommits,
		"Task branch family: "+env.exchange.Task.RefPrefix+"/*",
		"create at least one commit",
		"rerun ./procoder-return",
	)
}

func TestRunReturnFailsOutOfScopeBranchMutation(t *testing.T) {
	env := setupPreparedExportRepo(t)

	appendFile(t, filepath.Join(env.exportRoot, "README.md"), "task commit\n")
	runGit(t, env.exportRoot, "add", "README.md")
	runGit(t, env.exportRoot, "commit", "-m", "task commit")
	runGit(t, env.exportRoot, "branch", "-f", "main", "HEAD")

	_, err := Run(Options{CWD: env.exportRoot})
	assertErrorContains(t, err, errs.CodeRefOutOfScope,
		"outside the allowed task branch family",
		"refs/heads/main",
		env.exchange.Task.RefPrefix+"/*",
		"rerun ./procoder-return",
	)
}

func TestRunReturnFailsTagMutation(t *testing.T) {
	env := setupPreparedExportRepo(t)
	runGit(t, env.exportRoot, "tag", "v-new")

	_, err := Run(Options{CWD: env.exportRoot})
	assertErrorContains(t, err, errs.CodeRefOutOfScope,
		"Changed tags:",
		"refs/tags/v-new",
		"rerun ./procoder-return",
	)
}

func TestRunReturnFailsNonDescendantTaskRef(t *testing.T) {
	env := setupPreparedExportRepo(t)

	treeOID := strings.TrimSpace(runGit(t, env.exportRoot, "write-tree"))
	orphanOID := strings.TrimSpace(runGitInput(t, env.exportRoot, "orphan task ref\n", "commit-tree", treeOID))
	runGit(t, env.exportRoot, "update-ref", env.exchange.Task.RootRef, orphanOID)

	_, err := Run(Options{CWD: env.exportRoot})
	assertErrorContains(t, err, errs.CodeRefOutOfScope,
		"does not descend from the task base commit",
		"Ref: "+env.exchange.Task.RootRef,
		"rerun ./procoder-return",
	)
}

func TestRunReturnFailsMissingExchangeMetadata(t *testing.T) {
	env := setupPreparedExportRepo(t)
	exchangePath := filepath.Join(env.exportRoot, ".git", "procoder", "exchange.json")
	if err := os.Remove(exchangePath); err != nil {
		t.Fatalf("remove exchange metadata failed: %v", err)
	}

	_, err := Run(Options{CWD: env.exportRoot})
	assertErrorContains(t, err, errs.CodeInvalidExchange,
		"Path: .git/procoder/exchange.json",
		"run ./procoder-return only inside a repository created by procoder prepare",
	)
}

func TestRunReturnFailsInvalidExchangeMetadata(t *testing.T) {
	env := setupPreparedExportRepo(t)
	exchangePath := filepath.Join(env.exportRoot, ".git", "procoder", "exchange.json")
	writeFile(t, exchangePath, "{ invalid json\n")

	_, err := Run(Options{CWD: env.exportRoot})
	assertErrorContains(t, err, errs.CodeInvalidExchange,
		"Path: .git/procoder/exchange.json",
	)
}

type preparedEnv struct {
	sourceRepo *gitrepo.Repo
	exportRoot string
	exchange   exchange.Exchange
}

func setupPreparedExportRepo(t *testing.T) preparedEnv {
	t.Helper()

	source := gitrepo.New(t)
	source.WriteFile("README.md", "source content\n")
	source.CommitAll("initial")
	source.Git("branch", "feature/context")
	source.Git("tag", "v1.0.0")

	helperPath := writeHelperBinary(t)
	prepareResult, err := prepare.Run(prepare.Options{
		CWD:         source.Dir,
		ToolVersion: "0.1.0-test",
		HelperPath:  helperPath,
		Now:         func() time.Time { return time.Date(2026, time.March, 20, 11, 30, 15, 0, time.UTC) },
		Random:      bytes.NewReader([]byte{0x0a, 0x0b, 0x0c}),
	})
	if err != nil {
		t.Fatalf("prepare.Run failed: %v", err)
	}

	exportRoot := unzipTaskPackage(t, prepareResult.TaskPackagePath)
	ex, err := exchange.ReadExchange(filepath.Join(exportRoot, ".git", "procoder", "exchange.json"))
	if err != nil {
		t.Fatalf("ReadExchange(export) failed: %v", err)
	}

	return preparedEnv{
		sourceRepo: source,
		exportRoot: exportRoot,
		exchange:   ex,
	}
}

func assertErrorContains(t *testing.T, err error, wantCode errs.Code, contains ...string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error with code %s", wantCode)
	}
	typed, ok := errs.As(err)
	if !ok {
		t.Fatalf("expected typed error, got %T (%v)", err, err)
	}
	if typed.Code != wantCode {
		t.Fatalf("unexpected error code: got %s want %s", typed.Code, wantCode)
	}

	formatted := output.FormatError(err)
	for _, fragment := range contains {
		if !strings.Contains(formatted, fragment) {
			t.Fatalf("expected error output to contain %q, got:\n%s", fragment, formatted)
		}
	}
}

func writeHelperBinary(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "procoder-return")
	writeFile(t, path, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod helper failed: %v", err)
	}
	return path
}

func listZipEntries(t *testing.T, zipPath string) []string {
	t.Helper()

	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("zip.OpenReader failed: %v", err)
	}
	defer reader.Close()

	names := make([]string, 0, len(reader.File))
	for _, f := range reader.File {
		names = append(names, f.Name)
	}
	return names
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

func unzipTaskPackage(t *testing.T, zipPath string) string {
	t.Helper()

	dest := unzipFile(t, zipPath)
	return filepath.Join(dest, filepath.Base(filepath.Dir(zipPath)))
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

func runGitInput(t *testing.T, dir, input string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s failed:\nstdout:\n%s\nstderr:\n%s\nerr:%v", strings.Join(args, " "), stdout.String(), stderr.String(), err)
	}
	return stdout.String()
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

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s failed: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s failed: %v", path, err)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s failed: %v", path, err)
	}
	return string(data)
}

func findUpdate(updates []exchange.RefUpdate, ref string) (exchange.RefUpdate, bool) {
	for _, update := range updates {
		if update.Ref == ref {
			return update, true
		}
	}
	return exchange.RefUpdate{}, false
}
