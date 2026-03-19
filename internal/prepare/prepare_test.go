package prepare

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

	"github.com/amxv/procoder/internal/errs"
	"github.com/amxv/procoder/internal/exchange"
	"github.com/amxv/procoder/internal/testutil/gitrepo"
)

func TestRunPrepareHappyPath(t *testing.T) {
	repo := gitrepo.New(t)
	repo.WriteFile("README.md", "hello\n")
	headOID := strings.TrimSpace(repo.CommitAll("initial commit"))
	repo.Git("branch", "feature/alpha")
	repo.Git("tag", "v1.2.3")

	headRefBefore := strings.TrimSpace(repo.Git("symbolic-ref", "--quiet", "HEAD"))

	helperPath := writeHelperBinary(t)
	fixedNow := time.Date(2026, time.March, 20, 11, 30, 15, 0, time.UTC)
	result, err := Run(Options{
		CWD:         repo.Dir,
		ToolVersion: "0.1.0-test",
		HelperPath:  helperPath,
		Now:         func() time.Time { return fixedNow },
		Random:      bytes.NewReader([]byte{0x01, 0x02, 0x03}),
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	const expectedID = "20260320-113015-010203"
	expectedTaskRef := "refs/heads/procoder/" + expectedID + "/task"
	expectedTaskPrefix := "refs/heads/procoder/" + expectedID
	expectedTaskPackageName := "procoder-task-" + expectedID + ".zip"
	expectedTaskPackagePath := filepath.Join(repo.Dir, expectedTaskPackageName)
	if result.ExchangeID != expectedID {
		t.Fatalf("unexpected exchange id: got %q want %q", result.ExchangeID, expectedID)
	}
	if result.TaskRootRef != expectedTaskRef {
		t.Fatalf("unexpected task ref: got %q want %q", result.TaskRootRef, expectedTaskRef)
	}

	if filepath.Base(result.TaskPackagePath) != expectedTaskPackageName {
		t.Fatalf("unexpected task package filename: got %q want %q", filepath.Base(result.TaskPackagePath), expectedTaskPackageName)
	}
	if !filepath.IsAbs(result.TaskPackagePath) {
		t.Fatalf("expected absolute task package path, got %q", result.TaskPackagePath)
	}
	gotInfo, err := os.Stat(result.TaskPackagePath)
	if err != nil {
		t.Fatalf("expected task package zip to exist at %q: %v", result.TaskPackagePath, err)
	}
	wantInfo, err := os.Stat(expectedTaskPackagePath)
	if err != nil {
		t.Fatalf("expected task package zip to exist at %q: %v", expectedTaskPackagePath, err)
	}
	if !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("task package was not written to repo root: got %q want %q", result.TaskPackagePath, expectedTaskPackagePath)
	}
	sourceStatus := strings.TrimSpace(runGit(t, repo.Dir, "status", "--porcelain=v1", "--untracked-files=all"))
	if sourceStatus != "" {
		t.Fatalf("expected source repo to be clean after prepare, got status:\n%s", sourceStatus)
	}

	headRefAfter := strings.TrimSpace(repo.Git("symbolic-ref", "--quiet", "HEAD"))
	if headRefAfter != headRefBefore {
		t.Fatalf("current checkout changed: before %q after %q", headRefBefore, headRefAfter)
	}

	taskRefOID := strings.TrimSpace(repo.Git("rev-parse", expectedTaskRef))
	if taskRefOID != headOID {
		t.Fatalf("task branch points to unexpected commit: got %q want %q", taskRefOID, headOID)
	}

	localExchangePath := filepath.Join(repo.Dir, ".git", "procoder", "exchanges", expectedID, "exchange.json")
	localExchange, err := exchange.ReadExchange(localExchangePath)
	if err != nil {
		t.Fatalf("ReadExchange(local) failed: %v", err)
	}
	if localExchange.ExchangeID != expectedID {
		t.Fatalf("unexpected local exchange id: got %q want %q", localExchange.ExchangeID, expectedID)
	}
	if localExchange.Task.RootRef != expectedTaskRef {
		t.Fatalf("unexpected local task ref: got %q want %q", localExchange.Task.RootRef, expectedTaskRef)
	}
	if localExchange.Task.RefPrefix != expectedTaskPrefix {
		t.Fatalf("unexpected local task ref prefix: got %q want %q", localExchange.Task.RefPrefix, expectedTaskPrefix)
	}

	expectedHeads := readRefsInRepo(t, repo.Dir, "refs/heads")
	expectedTags := readRefsInRepo(t, repo.Dir, "refs/tags")

	extractedRoot := unzipTaskPackage(t, result.TaskPackagePath)
	exportExchangePath := filepath.Join(extractedRoot, ".git", "procoder", "exchange.json")
	exportExchange, err := exchange.ReadExchange(exportExchangePath)
	if err != nil {
		t.Fatalf("ReadExchange(export) failed: %v", err)
	}
	if exportExchange.ExchangeID != expectedID {
		t.Fatalf("unexpected exported exchange id: got %q want %q", exportExchange.ExchangeID, expectedID)
	}

	exportHeadRef := strings.TrimSpace(runGit(t, extractedRoot, "symbolic-ref", "--quiet", "HEAD"))
	if exportHeadRef != expectedTaskRef {
		t.Fatalf("export repo is on wrong branch: got %q want %q", exportHeadRef, expectedTaskRef)
	}
	exportStatus := strings.TrimSpace(runGit(t, extractedRoot, "status", "--porcelain=v1", "--untracked-files=all"))
	if exportStatus != "" {
		t.Fatalf("expected exported repo to be clean after prepare, got status:\n%s", exportStatus)
	}
	exportUserName := strings.TrimSpace(runGit(t, extractedRoot, "config", "--get", "user.name"))
	if exportUserName == "" {
		t.Fatalf("expected export user.name to be configured")
	}
	exportUserEmail := strings.TrimSpace(runGit(t, extractedRoot, "config", "--get", "user.email"))
	if exportUserEmail == "" {
		t.Fatalf("expected export user.email to be configured")
	}
	exportGPGSign := strings.TrimSpace(runGit(t, extractedRoot, "config", "--get", "commit.gpgsign"))
	if exportGPGSign != "false" {
		t.Fatalf("expected export commit.gpgsign=false, got %q", exportGPGSign)
	}

	gotHeads := readRefsInRepo(t, extractedRoot, "refs/heads")
	gotTags := readRefsInRepo(t, extractedRoot, "refs/tags")
	if diff := diffRefMaps(expectedHeads, gotHeads); diff != "" {
		t.Fatalf("heads mismatch:\n%s", diff)
	}
	if diff := diffRefMaps(expectedTags, gotTags); diff != "" {
		t.Fatalf("tags mismatch:\n%s", diff)
	}

	helperExportPath := filepath.Join(extractedRoot, "procoder-return")
	helperInfo, err := os.Stat(helperExportPath)
	if err != nil {
		t.Fatalf("expected exported helper at %q: %v", helperExportPath, err)
	}
	if helperInfo.Mode()&0o111 == 0 {
		t.Fatalf("expected exported helper to be executable, mode=%o", helperInfo.Mode())
	}

	sourceExclude := mustReadFile(t, filepath.Join(repo.Dir, ".git", "info", "exclude"))
	if !strings.Contains(sourceExclude, expectedTaskPackageName) {
		t.Fatalf("expected source exclude to contain task package name %q, got:\n%s", expectedTaskPackageName, sourceExclude)
	}

	exportExclude := mustReadFile(t, filepath.Join(extractedRoot, ".git", "info", "exclude"))
	for _, expectedPattern := range []string{"procoder-return", "procoder-return-*.zip"} {
		if !strings.Contains(exportExclude, expectedPattern) {
			t.Fatalf("expected export exclude to contain %q, got:\n%s", expectedPattern, exportExclude)
		}
	}

	if _, err := os.Stat(filepath.Join(extractedRoot, ".git", "hooks")); !os.IsNotExist(err) {
		t.Fatalf("expected export hooks directory to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(extractedRoot, ".git", "logs")); !os.IsNotExist(err) {
		t.Fatalf("expected export logs directory to be removed, stat err=%v", err)
	}
}

func TestRunPrepareFailsNotGitWorktree(t *testing.T) {
	helperPath := writeHelperBinary(t)
	_, err := Run(Options{
		CWD:        t.TempDir(),
		HelperPath: helperPath,
	})
	assertCode(t, err, errs.CodeNotGitRepo)
}

func TestRunPrepareFailsDirtyRepo(t *testing.T) {
	repo := gitrepo.New(t)
	repo.WriteFile("README.md", "one\n")
	repo.CommitAll("initial")
	repo.WriteFile("README.md", "two\n")

	helperPath := writeHelperBinary(t)
	_, err := Run(Options{
		CWD:        repo.Dir,
		HelperPath: helperPath,
	})
	assertCode(t, err, errs.CodeWorktreeDirty)
}

func TestRunPrepareFailsUntrackedFiles(t *testing.T) {
	repo := gitrepo.New(t)
	repo.WriteFile("README.md", "one\n")
	repo.CommitAll("initial")
	repo.WriteFile("scratch.txt", "untracked\n")

	helperPath := writeHelperBinary(t)
	_, err := Run(Options{
		CWD:        repo.Dir,
		HelperPath: helperPath,
	})
	assertCode(t, err, errs.CodeUntrackedFilesPresent)
}

func TestRunPrepareFailsWithSubmodules(t *testing.T) {
	repo := gitrepo.New(t)
	repo.WriteFile("README.md", "one\n")
	repo.CommitAll("initial")

	subRepo := gitrepo.New(t)
	subRepo.WriteFile("mod.txt", "submodule\n")
	subHeadOID := strings.TrimSpace(subRepo.CommitAll("submodule commit"))

	repo.Git("update-index", "--add", "--cacheinfo", "160000,"+subHeadOID+",vendor/submodule")
	repo.Git("commit", "-m", "add submodule gitlink")

	helperPath := writeHelperBinary(t)
	_, err := Run(Options{
		CWD:        repo.Dir,
		HelperPath: helperPath,
	})
	assertCode(t, err, errs.CodeSubmodulesUnsupported)
}

func TestRunPrepareFailsWhenLFSIsDetected(t *testing.T) {
	repo := gitrepo.New(t)
	repo.WriteFile("README.md", "one\n")
	repo.CommitAll("initial")
	repo.WriteFile(".gitattributes", "*.bin filter=lfs diff=lfs merge=lfs -text\n")
	repo.CommitAll("enable lfs")

	helperPath := writeHelperBinary(t)
	_, err := Run(Options{
		CWD:        repo.Dir,
		HelperPath: helperPath,
	})
	assertCode(t, err, errs.CodeLFSUnsupported)
}

func assertCode(t *testing.T, err error, want errs.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s", want)
	}
	typed, ok := errs.As(err)
	if !ok {
		t.Fatalf("expected typed error, got %T (%v)", err, err)
	}
	if typed.Code != want {
		t.Fatalf("unexpected error code: got %s want %s\nerror: %v", typed.Code, want, err)
	}
}

func writeHelperBinary(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "procoder-return")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write helper binary failed: %v", err)
	}
	return path
}

func unzipTaskPackage(t *testing.T, zipPath string) string {
	t.Helper()

	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("zip.OpenReader failed: %v", err)
	}
	defer reader.Close()

	dest := t.TempDir()
	for _, file := range reader.File {
		targetPath := filepath.Join(dest, file.Name)
		cleanDest := filepath.Clean(dest) + string(os.PathSeparator)
		if !strings.HasPrefix(filepath.Clean(targetPath), cleanDest) {
			t.Fatalf("zip entry escapes destination: %q", file.Name)
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				t.Fatalf("mkdir for zip dir failed: %v", err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			t.Fatalf("mkdir for zip file failed: %v", err)
		}
		in, err := file.Open()
		if err != nil {
			t.Fatalf("open zip file failed: %v", err)
		}
		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, file.Mode())
		if err != nil {
			in.Close()
			t.Fatalf("create extracted file failed: %v", err)
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			_ = in.Close()
			t.Fatalf("copy extracted file failed: %v", err)
		}
		_ = out.Close()
		_ = in.Close()
	}

	return filepath.Join(dest, filepath.Base(filepath.Dir(zipPath)))
}

func readRefsInRepo(t *testing.T, dir, namespace string) map[string]string {
	t.Helper()

	out := strings.TrimSpace(runGit(t, dir, "for-each-ref", "--format=%(refname) %(objectname)", namespace))
	refs := make(map[string]string)
	if out == "" {
		return refs
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		refs[fields[0]] = fields[1]
	}
	return refs
}

func diffRefMaps(want, got map[string]string) string {
	var lines []string
	for ref, wantOID := range want {
		gotOID, ok := got[ref]
		if !ok {
			lines = append(lines, "missing "+ref)
			continue
		}
		if gotOID != wantOID {
			lines = append(lines, "mismatch "+ref+": got "+gotOID+" want "+wantOID)
		}
	}
	for ref := range got {
		if _, ok := want[ref]; !ok {
			lines = append(lines, "unexpected "+ref)
		}
	}
	return strings.Join(lines, "\n")
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

func mustReadFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s failed: %v", path, err)
	}
	return string(data)
}
