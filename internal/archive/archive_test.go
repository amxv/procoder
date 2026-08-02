package archive

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/procoder/internal/errs"
	"github.com/amxv/procoder/internal/testutil/gitrepo"
)

func TestRunArchiveIncludesWorktreeAndGitHistoryWithoutChangingSource(t *testing.T) {
	repo := gitrepo.New(t)
	repo.WriteFile(".gitignore", "ignored/\n*.cache\n")
	repo.WriteFile("app.txt", "initial\n")
	firstOID := strings.TrimSpace(repo.CommitAll("initial commit"))
	repo.WriteFile("app.txt", "second\n")
	secondOID := strings.TrimSpace(repo.CommitAll("second commit"))
	repo.Git("branch", "feature/review")
	repo.Git("tag", "v1.0.0")

	refsBefore := repo.Git("for-each-ref", "--format=%(refname) %(objectname)")
	excludePath := filepath.Join(repo.Dir, ".git", "info", "exclude")
	excludeBefore := readFileIfExists(t, excludePath)
	repo.WriteFile("app.txt", "working tree change\n")
	repo.WriteFile("review.txt", "untracked review note\n")
	repo.WriteFile("staged.txt", "staged change\n")
	repo.Git("add", "staged.txt")
	repo.WriteFile("ignored/build/output.bin", "ignored artifact\n")
	repo.WriteFile("cache.cache", "ignored cache\n")
	statusBefore := repo.Git("status", "--porcelain=v1", "--untracked-files=all")

	var revealedPath string
	result, err := Run(Options{
		RepoPath: repo.Dir,
		Open:     true,
		TempDir:  t.TempDir(),
		Now:      func() time.Time { return time.Date(2026, time.August, 2, 12, 34, 56, 0, time.UTC) },
		Reveal: func(path string) error {
			revealedPath = path
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Opened {
		t.Fatalf("expected archive to be marked opened: %#v", result)
	}
	if revealedPath != result.ArchivePath {
		t.Fatalf("reveal path mismatch: got %q want %q", revealedPath, result.ArchivePath)
	}
	if !strings.HasSuffix(result.ArchivePath, "-20260802-123456.zip") {
		t.Fatalf("unexpected archive filename: %q", result.ArchivePath)
	}
	if !filepath.IsAbs(result.ArchivePath) {
		t.Fatalf("expected absolute archive path, got %q", result.ArchivePath)
	}
	if _, err := os.Stat(result.ArchivePath); err != nil {
		t.Fatalf("archive does not exist: %v", err)
	}

	entries := readZipEntries(t, result.ArchivePath)
	rootPrefix := filepath.Base(repo.Dir) + "/"
	for _, rel := range []string{".gitignore", "app.txt", "review.txt", "staged.txt", ".git/HEAD"} {
		if !entries[rootPrefix+filepath.ToSlash(rel)] {
			t.Errorf("archive is missing %q", rel)
		}
	}
	for _, rel := range []string{"ignored/build/output.bin", "cache.cache"} {
		if entries[rootPrefix+filepath.ToSlash(rel)] {
			t.Errorf("archive unexpectedly includes ignored file %q", rel)
		}
	}

	extractedRoot := extractArchive(t, result.ArchivePath)
	archiveRoot := filepath.Join(extractedRoot, filepath.Base(repo.Dir))
	if got := mustReadFile(t, filepath.Join(archiveRoot, "app.txt")); got != "working tree change\n" {
		t.Fatalf("working tree change was not archived: got %q", got)
	}
	if got := mustReadFile(t, filepath.Join(archiveRoot, "review.txt")); got != "untracked review note\n" {
		t.Fatalf("untracked file was not archived: got %q", got)
	}

	if got := strings.TrimSpace(runGit(t, archiveRoot, "rev-parse", "HEAD")); got != secondOID {
		t.Fatalf("archive HEAD mismatch: got %q want %q", got, secondOID)
	}
	log := runGit(t, archiveRoot, "log", "--format=%s", "--all")
	for _, message := range []string{"initial commit", "second commit"} {
		if !strings.Contains(log, message) {
			t.Errorf("archive history is missing %q: %s", message, log)
		}
	}
	branches := runGit(t, archiveRoot, "for-each-ref", "--format=%(refname)", "refs/heads")
	for _, branch := range []string{"refs/heads/main", "refs/heads/feature/review"} {
		if !strings.Contains(branches, branch) {
			t.Errorf("archive is missing branch %q: %s", branch, branches)
		}
	}
	if got := strings.TrimSpace(runGit(t, archiveRoot, "tag")); got != "v1.0.0" {
		t.Fatalf("archive tags mismatch: got %q", got)
	}
	if got := strings.TrimSpace(runGit(t, archiveRoot, "remote")); got != "" {
		t.Fatalf("archive unexpectedly retained remotes: %q", got)
	}
	if _, err := os.Stat(filepath.Join(archiveRoot, ".git", "hooks")); !os.IsNotExist(err) {
		t.Fatalf("archive unexpectedly retained hooks: %v", err)
	}
	if _, err := os.Stat(filepath.Join(archiveRoot, ".git", "logs")); !os.IsNotExist(err) {
		t.Fatalf("archive unexpectedly retained reflogs: %v", err)
	}

	if got := repo.Git("status", "--porcelain=v1", "--untracked-files=all"); got != statusBefore {
		t.Fatalf("source status changed:\nbefore:\n%safter:\n%s", statusBefore, got)
	}
	if got := repo.Git("for-each-ref", "--format=%(refname) %(objectname)"); got != refsBefore {
		t.Fatalf("source refs changed:\nbefore:\n%safter:\n%s", refsBefore, got)
	}
	if got := readFileIfExists(t, excludePath); got != excludeBefore {
		t.Fatalf("source Git exclude file changed:\nbefore:\n%safter:\n%s", excludeBefore, got)
	}
	if strings.Contains(repo.Git("for-each-ref", "--format=%(refname)"), "refs/heads/procoder/") {
		t.Fatal("archive created a procoder branch in the source repository")
	}
	if firstOID == secondOID {
		t.Fatal("test setup did not create distinct commits")
	}
}

func TestRunArchiveSupportsDetachedHead(t *testing.T) {
	repo := gitrepo.New(t)
	repo.WriteFile("app.txt", "one\n")
	headOID := strings.TrimSpace(repo.CommitAll("initial"))
	repo.Git("checkout", "--detach", headOID)

	result, err := Run(Options{RepoPath: repo.Dir, TempDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	archiveRoot := filepath.Join(extractArchive(t, result.ArchivePath), filepath.Base(repo.Dir))
	if got := strings.TrimSpace(runGit(t, archiveRoot, "rev-parse", "HEAD")); got != headOID {
		t.Fatalf("detached archive HEAD mismatch: got %q want %q", got, headOID)
	}
	if _, err := exec.Command("git", "-C", archiveRoot, "symbolic-ref", "--quiet", "HEAD").CombinedOutput(); err == nil {
		t.Fatal("expected detached archive HEAD")
	}
}

func TestRunArchiveRejectsSubmodules(t *testing.T) {
	repo := gitrepo.New(t)
	repo.WriteFile("README.md", "one\n")
	repo.CommitAll("initial")

	subRepo := gitrepo.New(t)
	subRepo.WriteFile("module.txt", "module\n")
	subOID := strings.TrimSpace(subRepo.CommitAll("module"))
	repo.Git("update-index", "--add", "--cacheinfo", "160000,"+subOID+",vendor/module")
	repo.Git("commit", "-m", "add submodule")

	_, err := Run(Options{RepoPath: repo.Dir, TempDir: t.TempDir()})
	assertArchiveErrorCode(t, err, errs.CodeSubmodulesUnsupported)
}

func TestRunArchiveRejectsLFS(t *testing.T) {
	repo := gitrepo.New(t)
	repo.WriteFile("README.md", "one\n")
	repo.CommitAll("initial")
	repo.WriteFile(".gitattributes", "*.bin filter=lfs diff=lfs merge=lfs -text\n")
	repo.CommitAll("enable lfs")

	_, err := Run(Options{RepoPath: repo.Dir, TempDir: t.TempDir()})
	assertArchiveErrorCode(t, err, errs.CodeLFSUnsupported)
}

func TestFormatSuccessIncludesArchivePathAndOpenWarning(t *testing.T) {
	got := FormatSuccess(Result{
		ArchivePath: "/tmp/procoder-archive-a1b2c3/repo-20260802-123456.zip",
		OpenError:   "open unavailable",
	})
	for _, expected := range []string{
		"Created repository archive.",
		"/tmp/procoder-archive-a1b2c3/repo-20260802-123456.zip",
		"archive created",
		"open unavailable",
	} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(expected)) {
			t.Errorf("formatted result is missing %q: %q", expected, got)
		}
	}
}

func readZipEntries(t *testing.T, path string) map[string]bool {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}
	defer reader.Close()

	entries := make(map[string]bool, len(reader.File))
	for _, file := range reader.File {
		entries[file.Name] = true
	}
	return entries
}

func extractArchive(t *testing.T, path string) string {
	t.Helper()
	dest := t.TempDir()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		name := filepath.FromSlash(file.Name)
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			t.Fatalf("unsafe zip entry %q", file.Name)
		}
		destination := filepath.Join(dest, name)
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				t.Fatalf("create extracted directory failed: %v", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			t.Fatalf("create extracted parent failed: %v", err)
		}
		in, err := file.Open()
		if err != nil {
			t.Fatalf("open extracted entry failed: %v", err)
		}
		out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			_ = in.Close()
			t.Fatalf("create extracted file failed: %v", err)
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		_ = in.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatalf("extract entry %q failed: copy=%v close=%v", file.Name, copyErr, closeErr)
		}
	}
	return dest
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
		t.Fatalf("git %s failed: %v\nstderr:\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String()
}

func readFileIfExists(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("read %s failed: %v", path, err)
	}
	return string(data)
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s failed: %v", path, err)
	}
	return string(data)
}

func assertArchiveErrorCode(t *testing.T, err error, want errs.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s", want)
	}
	if got := errs.CodeOf(err); got != want {
		t.Fatalf("unexpected error code: got %s want %s (%v)", got, want, err)
	}
}
