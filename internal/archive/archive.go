package archive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/amxv/procoder/internal/errs"
	"github.com/amxv/procoder/internal/gitx"
)

const (
	archiveDirPattern = "procoder-archive-*"
	archiveNameFormat = "%s-%s.zip"
	defaultArchiveTag = "repository"
)

// Options controls creation of a standalone repository archive.
type Options struct {
	// RepoPath is the repository or a directory inside it. An empty value uses
	// the current working directory.
	RepoPath string
	// Open reveals the created archive in the platform file manager.
	Open bool
	// TempDir overrides the system temporary directory. It is primarily useful
	// for tests.
	TempDir string
	// Now controls the timestamp used in the archive filename.
	Now func() time.Time
	// Reveal overrides the platform file manager command. It is primarily
	// useful for tests.
	Reveal func(string) error
}

// Result describes a created archive.
type Result struct {
	ArchivePath string
	RepoRoot    string
	Opened      bool
	OpenError   string
}

// Run creates a zip containing the current worktree and a portable copy of
// its local Git history. The source repository is never modified.
func Run(opts Options) (result Result, err error) {
	repoPath := strings.TrimSpace(opts.RepoPath)
	if repoPath == "" {
		repoPath, err = os.Getwd()
		if err != nil {
			return Result{}, errs.Wrap(errs.CodeInternal, "resolve current working directory", err)
		}
	}

	repoRoot, err := resolveRepo(repoPath)
	if err != nil {
		return Result{}, err
	}
	sourceGit := gitx.NewRunner(repoRoot)

	if err := validateArchiveSource(repoRoot, sourceGit); err != nil {
		return Result{}, err
	}

	headOID, err := readTrimmed(sourceGit, "rev-parse", "HEAD")
	if err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "resolve current HEAD commit", err)
	}
	headRef, err := readOptionalHeadRef(sourceGit)
	if err != nil {
		return Result{}, err
	}
	entries, err := listWorktreeEntries(sourceGit)
	if err != nil {
		return Result{}, err
	}

	tempParent := strings.TrimSpace(opts.TempDir)
	if tempParent == "" {
		tempParent = os.TempDir()
	}
	archiveDir, err := os.MkdirTemp(tempParent, archiveDirPattern)
	if err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "create archive temporary directory", err)
	}
	keepArchiveDir := false
	defer func() {
		if keepArchiveDir {
			return
		}
		_ = os.RemoveAll(archiveDir)
	}()

	repoName := filepath.Base(filepath.Clean(repoRoot))
	if strings.TrimSpace(repoName) == "" || repoName == "." || repoName == string(filepath.Separator) {
		repoName = defaultArchiveTag
	}
	stagingRoot := filepath.Join(archiveDir, repoName)
	if err := cloneRepository(repoRoot, stagingRoot, headOID, headRef); err != nil {
		return Result{}, err
	}

	if err := syncWorktree(repoRoot, stagingRoot, entries); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "copy repository worktree", err)
	}

	nowFn := opts.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	archiveName := fmt.Sprintf(
		archiveNameFormat,
		sanitizeFilename(repoName),
		nowFn().UTC().Format("20060102-150405"),
	)
	archivePath := filepath.Join(archiveDir, archiveName)
	if err := createZip(archivePath, stagingRoot, repoName); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "create repository archive zip", err)
	}
	if err := os.RemoveAll(stagingRoot); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "remove archive staging directory", err)
	}

	archivePath, err = filepath.Abs(archivePath)
	if err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "resolve archive path", err)
	}

	result = Result{
		ArchivePath: archivePath,
		RepoRoot:    repoRoot,
	}
	keepArchiveDir = true

	if opts.Open {
		reveal := opts.Reveal
		if reveal == nil {
			reveal = revealInFileManager
		}
		if openErr := reveal(archivePath); openErr != nil {
			result.OpenError = openErr.Error()
		} else {
			result.Opened = true
		}
	}

	return result, nil
}

// FormatSuccess formats the user-facing archive result.
func FormatSuccess(result Result) string {
	lines := []string{
		"Created repository archive.",
		fmt.Sprintf("Archive: %s", result.ArchivePath),
	}
	if result.Opened {
		if runtime.GOOS == "darwin" {
			lines = append(lines, "Opened in Finder.")
		} else {
			lines = append(lines, "Opened in the file manager.")
		}
	} else if result.OpenError != "" {
		lines = append(lines, "Warning: archive created, but the file manager could not be opened: "+result.OpenError)
	}
	return strings.Join(lines, "\n")
}

func resolveRepo(repoPath string) (string, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return "", errs.Wrap(errs.CodeInternal, "resolve repository path", err)
	}

	runner := gitx.NewRunner(absPath)
	result, err := runner.Run("rev-parse", "--show-toplevel")
	if err != nil {
		if errs.CodeOf(err) == errs.CodeGitUnavailable {
			return "", err
		}
		return "", errs.New(
			errs.CodeNotGitRepo,
			"repository path is not a Git worktree",
			errs.WithHint("run `procoder archive` inside a Git repository or pass a repository path"),
		)
	}

	root := strings.TrimSpace(result.Stdout)
	if root == "" {
		return "", errs.New(
			errs.CodeNotGitRepo,
			"repository path is not a Git worktree",
			errs.WithHint("run `procoder archive` inside a Git repository or pass a repository path"),
		)
	}
	return filepath.Abs(root)
}

func validateArchiveSource(repoRoot string, runner gitx.Runner) error {
	shallow, err := readTrimmed(runner, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return errs.Wrap(errs.CodeInternal, "inspect repository history depth", err)
	}
	if shallow == "true" {
		return errs.New(
			errs.CodeShallowRepository,
			"repository is shallow and does not contain its full Git history",
			errs.WithHint("fetch the complete history before running `procoder archive`"),
		)
	}

	submodules, err := detectSubmodules(runner)
	if err != nil {
		return err
	}
	if len(submodules) > 0 {
		sort.Strings(submodules)
		details := []string{"Submodule paths:"}
		for _, path := range submodules {
			details = append(details, "  "+path)
		}
		return errs.New(
			errs.CodeSubmodulesUnsupported,
			"submodules are not supported by `procoder archive`",
			errs.WithDetails(details...),
			errs.WithHint("remove submodule entries or archive those repositories separately"),
		)
	}

	lfsSignals, err := detectLFSSignals(repoRoot, runner)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, "detect Git LFS usage", err)
	}
	if len(lfsSignals) > 0 {
		sort.Strings(lfsSignals)
		details := []string{"Git LFS signals:"}
		for _, signal := range lfsSignals {
			details = append(details, "  "+signal)
		}
		return errs.New(
			errs.CodeLFSUnsupported,
			"Git LFS is not supported by `procoder archive`",
			errs.WithDetails(details...),
			errs.WithHint("remove Git LFS filters before running `procoder archive`"),
		)
	}

	return nil
}

func detectSubmodules(runner gitx.Runner) ([]string, error) {
	result, err := runner.Run("ls-files", "--stage")
	if err != nil {
		return nil, errs.Wrap(errs.CodeInternal, "inspect index entries for submodules", err)
	}

	var paths []string
	for _, line := range splitNonEmptyLines(result.Stdout) {
		stagePart, pathPart, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(stagePart)
		if len(fields) > 0 && fields[0] == "160000" {
			paths = append(paths, pathPart)
		}
	}
	return paths, nil
}

func detectLFSSignals(repoRoot string, runner gitx.Runner) ([]string, error) {
	var signals []string

	if _, err := os.Stat(filepath.Join(repoRoot, ".lfsconfig")); err == nil {
		signals = append(signals, ".lfsconfig")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	result, err := runner.Run(
		"ls-files",
		"--cached",
		"--others",
		"--exclude-standard",
		"-z",
		"--",
		"*.gitattributes",
	)
	if err != nil {
		return nil, err
	}
	for _, rel := range splitNUL(result.Stdout) {
		if rel == "" {
			continue
		}
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		content := string(data)
		if strings.Contains(content, "filter=lfs") || strings.Contains(content, "diff=lfs") || strings.Contains(content, "merge=lfs") {
			signals = append(signals, filepath.ToSlash(rel))
		}
	}
	return signals, nil
}

func readOptionalHeadRef(runner gitx.Runner) (string, error) {
	result, err := runner.Run("symbolic-ref", "--quiet", "HEAD")
	if err == nil {
		return strings.TrimSpace(result.Stdout), nil
	}
	if result.ExitCode == 1 {
		return "", nil
	}
	return "", errs.Wrap(errs.CodeInternal, "resolve current branch", err)
}

func listWorktreeEntries(runner gitx.Runner) ([]string, error) {
	result, err := runner.Run("ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, errs.Wrap(errs.CodeInternal, "list repository files", err)
	}

	entries := make([]string, 0)
	for _, entry := range splitNUL(result.Stdout) {
		if entry != "" {
			entries = append(entries, entry)
		}
	}
	sort.Strings(entries)
	return entries, nil
}

func cloneRepository(sourceRoot, targetRoot, headOID, headRef string) error {
	parent := filepath.Dir(targetRoot)
	if _, err := gitx.NewRunner(parent).Run("clone", "--no-local", "--no-checkout", sourceRoot, targetRoot); err != nil {
		return errs.Wrap(errs.CodeInternal, "clone repository history into archive", err)
	}

	runner := gitx.NewRunner(targetRoot)
	if headRef == "" {
		if _, err := runner.Run("fetch", "--no-tags", sourceRoot, headOID); err != nil {
			return errs.Wrap(errs.CodeInternal, "copy detached HEAD into archive", err)
		}
	}
	if _, err := runner.Run("checkout", "--quiet", "--detach", headOID); err != nil {
		return errs.Wrap(errs.CodeInternal, "prepare archive worktree", err)
	}
	if _, err := runner.Run("fetch", "--no-tags", sourceRoot, "+refs/heads/*:refs/heads/*"); err != nil {
		return errs.Wrap(errs.CodeInternal, "copy local branches into archive", err)
	}
	if _, err := runner.Run("fetch", "--no-tags", sourceRoot, "+refs/tags/*:refs/tags/*"); err != nil {
		return errs.Wrap(errs.CodeInternal, "copy local tags into archive", err)
	}
	if headRef != "" {
		if _, err := runner.Run("symbolic-ref", "HEAD", headRef); err != nil {
			return errs.Wrap(errs.CodeInternal, "preserve current branch in archive", err)
		}
	}
	if err := removeRemotes(runner); err != nil {
		return err
	}

	gitDir, err := resolveGitDir(targetRoot)
	if err != nil {
		return err
	}
	if err := stripGitState(gitDir); err != nil {
		return errs.Wrap(errs.CodeInternal, "sanitize archive Git metadata", err)
	}
	return nil
}

func removeRemotes(runner gitx.Runner) error {
	result, err := runner.Run("remote")
	if err != nil {
		return errs.Wrap(errs.CodeInternal, "inspect archive remotes", err)
	}
	for _, remote := range splitNonEmptyLines(result.Stdout) {
		if _, err := runner.Run("remote", "remove", remote); err != nil {
			return errs.Wrap(errs.CodeInternal, "remove archive remote", err)
		}
	}
	return nil
}

func resolveGitDir(repoRoot string) (string, error) {
	result, err := gitx.NewRunner(repoRoot).Run("rev-parse", "--git-dir")
	if err != nil {
		return "", errs.Wrap(errs.CodeInternal, "resolve archive Git directory", err)
	}
	gitDir := strings.TrimSpace(result.Stdout)
	if filepath.IsAbs(gitDir) {
		return filepath.Clean(gitDir), nil
	}
	return filepath.Clean(filepath.Join(repoRoot, gitDir)), nil
}

func stripGitState(gitDir string) error {
	for _, rel := range []string{"hooks", "logs", "refs/remotes"} {
		if err := os.RemoveAll(filepath.Join(gitDir, rel)); err != nil {
			return err
		}
	}
	for _, name := range []string{"FETCH_HEAD", "ORIG_HEAD", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REBASE_HEAD"} {
		if err := os.Remove(filepath.Join(gitDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func syncWorktree(sourceRoot, targetRoot string, entries []string) error {
	for _, rel := range entries {
		sourcePath, err := safeJoin(sourceRoot, rel)
		if err != nil {
			return err
		}
		targetPath, err := safeJoin(targetRoot, rel)
		if err != nil {
			return err
		}

		if _, err := os.Lstat(sourcePath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if err := removePath(targetPath); err != nil {
					return err
				}
				continue
			}
			return err
		}
		if err := copyPath(sourcePath, targetPath); err != nil {
			return err
		}
	}
	return nil
}

func safeJoin(root, rel string) (string, error) {
	rel = filepath.FromSlash(rel)
	clean := filepath.Clean(rel)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid repository path %q", rel)
	}
	if clean == ".git" || strings.HasPrefix(clean, ".git"+string(filepath.Separator)) {
		return "", fmt.Errorf("repository file path %q overlaps Git metadata", rel)
	}
	return filepath.Join(root, clean), nil
}

func copyPath(sourcePath, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	if err := removePath(targetPath); err != nil {
		return err
	}

	info, err := os.Lstat(sourcePath)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(sourcePath)
		if err != nil {
			return err
		}
		if err := os.Symlink(target, targetPath); err != nil {
			return err
		}
	case info.Mode().IsRegular():
		if err := copyFile(sourcePath, targetPath, info.Mode().Perm()); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported repository file type at %q", sourcePath)
	}
	return nil
}

func copyFile(sourcePath, targetPath string, mode os.FileMode) error {
	in, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Chmod(mode); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func removePath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

func createZip(targetZip, sourceDir, rootPrefix string) error {
	out, err := os.OpenFile(targetZip, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(out)

	walkErr := filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		zipPath := filepath.ToSlash(filepath.Join(rootPrefix, rel))
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = zipPath
		if info.IsDir() {
			header.Name += "/"
			header.Method = zip.Store
		} else if info.Mode()&os.ModeSymlink != 0 {
			header.Method = zip.Store
		} else {
			header.Method = zip.Deflate
		}

		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_, err = io.WriteString(writer, target)
			return err
		}

		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(writer, in)
		return err
	})
	if walkErr != nil {
		_ = zw.Close()
		_ = out.Close()
		return walkErr
	}
	if err := zw.Close(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func revealInFileManager(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", path)
	case "windows":
		cmd = exec.Command("explorer.exe", "/select,", filepath.FromSlash(path))
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(path))
	}
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func sanitizeFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return defaultArchiveTag
	}
	return b.String()
}

func readTrimmed(runner gitx.Runner, args ...string) (string, error) {
	result, err := runner.Run(args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func splitNonEmptyLines(value string) []string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func splitNUL(value string) []string {
	return strings.Split(value, "\x00")
}
