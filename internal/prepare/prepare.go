package prepare

import (
	"archive/zip"
	"bufio"
	"crypto/rand"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/amxv/procoder/internal/errs"
	"github.com/amxv/procoder/internal/exchange"
	"github.com/amxv/procoder/internal/gitx"
)

const (
	helperEnvVar        = "PROCODER_RETURN_HELPER"
	defaultToolVersion  = "dev"
	defaultHelperName   = "procoder-return"
	defaultHelperAsset  = "procoder-return_linux_amd64"
	defaultReturnGlob   = "procoder-return-*.zip"
	defaultTaskNameFmt  = "procoder-task-%s.zip"
	defaultUserName     = "procoder"
	defaultUserEmail    = "procoder@local"
	successMessageIntro = "Prepared exchange."
)

type Options struct {
	CWD         string
	ToolVersion string
	HelperPath  string
	Now         func() time.Time
	Random      io.Reader
}

type Result struct {
	ExchangeID      string
	TaskRootRef     string
	TaskPackagePath string
}

func Run(opts Options) (Result, error) {
	cwd, err := resolveCWD(opts.CWD)
	if err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "resolve current working directory", err)
	}

	repoRoot, gitDir, err := resolveRepo(cwd)
	if err != nil {
		return Result{}, err
	}
	sourceGit := gitx.NewRunner(repoRoot)

	if err := validateSourceRepo(repoRoot, sourceGit); err != nil {
		return Result{}, err
	}

	headRef, err := readTrimmed(sourceGit, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return Result{}, errs.Wrap(
			errs.CodeInternal,
			"resolve current branch",
			err,
			errs.WithHint("check that HEAD points to a branch and retry"),
		)
	}
	headOID, err := readTrimmed(sourceGit, "rev-parse", "HEAD")
	if err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "resolve current HEAD commit", err)
	}

	heads, err := readRefSnapshot(sourceGit, "refs/heads")
	if err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "read local branch snapshot", err)
	}
	tags, err := readRefSnapshot(sourceGit, "refs/tags")
	if err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "read local tag snapshot", err)
	}

	nowFn := opts.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	nowUTC := nowFn().UTC()
	random := opts.Random
	if random == nil {
		random = rand.Reader
	}

	exchangeID, err := exchange.GenerateID(nowUTC, random)
	if err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "generate exchange id", err)
	}
	taskRootRef := exchange.TaskRootRef(exchangeID)
	if taskRootRef == "" {
		return Result{}, errs.New(errs.CodeInternal, "generated an invalid exchange id")
	}
	taskRefPrefix := exchange.TaskRefPrefix(exchangeID)
	if taskRefPrefix == "" {
		return Result{}, errs.New(errs.CodeInternal, "generated an invalid task ref prefix")
	}

	if err := createTaskBranch(sourceGit, taskRootRef, headOID); err != nil {
		return Result{}, err
	}

	toolVersion := strings.TrimSpace(opts.ToolVersion)
	if toolVersion == "" {
		toolVersion = defaultToolVersion
	}

	exchangeRecord := exchange.Exchange{
		Protocol:    exchange.ExchangeProtocolV1,
		ExchangeID:  exchangeID,
		CreatedAt:   nowUTC,
		ToolVersion: toolVersion,
		Source: exchange.ExchangeSource{
			HeadRef: headRef,
			HeadOID: headOID,
		},
		Task: exchange.ExchangeTask{
			RootRef:   taskRootRef,
			RefPrefix: taskRefPrefix,
			BaseOID:   headOID,
		},
		Context: exchange.ExchangeContext{
			Heads: heads,
			Tags:  tags,
		},
	}

	localExchangePath := filepath.Join(gitDir, "procoder", "exchanges", exchangeID, "exchange.json")
	if err := exchange.WriteExchange(localExchangePath, exchangeRecord); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "write local exchange record", err)
	}

	tempDir, err := os.MkdirTemp("", "procoder-export-*")
	if err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "create temporary export directory", err)
	}
	defer os.RemoveAll(tempDir)

	exportRoot := filepath.Join(tempDir, filepath.Base(repoRoot))
	if err := os.MkdirAll(exportRoot, 0o755); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "create export repository path", err)
	}
	exportGit := gitx.NewRunner(exportRoot)

	if err := initExportRepo(exportGit); err != nil {
		return Result{}, err
	}
	if err := fetchContextRefs(exportGit, repoRoot); err != nil {
		return Result{}, err
	}

	exportGitDir, err := resolveGitDir(exportRoot)
	if err != nil {
		return Result{}, err
	}
	if err := stripGitState(exportGitDir); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "sanitize export git metadata", err)
	}

	if err := configureCommitIdentity(sourceGit, exportGit); err != nil {
		return Result{}, err
	}
	if _, err := exportGit.Run("config", "commit.gpgsign", "false"); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "configure export commit.gpgsign", err)
	}

	shortTaskRef := strings.TrimPrefix(taskRootRef, "refs/heads/")
	if _, err := exportGit.Run("checkout", "--quiet", shortTaskRef); err != nil {
		return Result{}, errs.Wrap(
			errs.CodeInternal,
			fmt.Sprintf("check out task branch %s in export repository", shortTaskRef),
			err,
		)
	}

	exportExchangePath := filepath.Join(exportGitDir, "procoder", "exchange.json")
	if err := exchange.WriteExchange(exportExchangePath, exchangeRecord); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "write exported exchange record", err)
	}

	helperPath, err := resolveHelperBinary(opts.HelperPath)
	if err != nil {
		return Result{}, err
	}
	exportHelperPath := filepath.Join(exportRoot, defaultHelperName)
	if err := copyExecutable(helperPath, exportHelperPath); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "inject procoder-return helper", err)
	}
	if err := appendExclude(exportGitDir, defaultHelperName, defaultReturnGlob); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "update export .git/info/exclude", err)
	}
	if err := stripGitState(exportGitDir); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "finalize export git sanitization", err)
	}

	taskPackageName := fmt.Sprintf(defaultTaskNameFmt, exchangeID)
	taskPackagePath := filepath.Join(repoRoot, taskPackageName)
	if err := createZip(taskPackagePath, exportRoot, filepath.Base(repoRoot)); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "create task package zip", err)
	}
	if err := appendExclude(gitDir, taskPackageName); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "update source .git/info/exclude", err)
	}

	absTaskPackage, err := filepath.Abs(taskPackagePath)
	if err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "resolve absolute task package path", err)
	}

	return Result{
		ExchangeID:      exchangeID,
		TaskRootRef:     taskRootRef,
		TaskPackagePath: absTaskPackage,
	}, nil
}

func FormatSuccess(result Result) string {
	return strings.Join([]string{
		successMessageIntro,
		fmt.Sprintf("Task branch: %s", result.TaskRootRef),
		fmt.Sprintf("Task package: %s", result.TaskPackagePath),
	}, "\n")
}

func resolveCWD(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return os.Getwd()
	}
	return cwd, nil
}

func resolveRepo(cwd string) (string, string, error) {
	root, err := readTrimmed(gitx.NewRunner(cwd), "rev-parse", "--show-toplevel")
	if err != nil {
		if isNotGitRepoError(err) {
			return "", "", errs.New(
				errs.CodeNotGitRepo,
				"current directory is not a Git worktree",
				errs.WithHint("run `procoder prepare` inside a Git repository"),
			)
		}
		return "", "", errs.Wrap(errs.CodeInternal, "resolve repository root", err)
	}

	gitDir, err := resolveGitDir(root)
	if err != nil {
		return "", "", err
	}
	return root, gitDir, nil
}

func resolveGitDir(repoRoot string) (string, error) {
	gitDirRaw, err := readTrimmed(gitx.NewRunner(repoRoot), "rev-parse", "--git-dir")
	if err != nil {
		if isNotGitRepoError(err) {
			return "", errs.New(
				errs.CodeNotGitRepo,
				"current directory is not a Git worktree",
				errs.WithHint("run `procoder prepare` inside a Git repository"),
			)
		}
		return "", errs.Wrap(errs.CodeInternal, "resolve repository git directory", err)
	}

	if filepath.IsAbs(gitDirRaw) {
		return filepath.Clean(gitDirRaw), nil
	}
	return filepath.Clean(filepath.Join(repoRoot, gitDirRaw)), nil
}

func validateSourceRepo(repoRoot string, runner gitx.Runner) error {
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
			"submodules are not supported in procoder v1",
			errs.WithDetails(details...),
			errs.WithHint("remove submodule entries before running `procoder prepare`"),
		)
	}

	status, err := runner.Run("status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		if isNotGitRepoError(err) {
			return errs.New(
				errs.CodeNotGitRepo,
				"current directory is not a Git worktree",
				errs.WithHint("run `procoder prepare` inside a Git repository"),
			)
		}
		return errs.Wrap(errs.CodeInternal, "read repository status", err)
	}

	var dirty []string
	var untracked []string
	for _, line := range splitNonEmptyLines(status.Stdout) {
		if strings.HasPrefix(line, "?? ") {
			untracked = append(untracked, strings.TrimPrefix(line, "?? "))
			continue
		}
		dirty = append(dirty, line)
	}

	if len(untracked) > 0 {
		sort.Strings(untracked)
		details := []string{"Untracked files:"}
		for _, path := range untracked {
			details = append(details, "  "+path)
		}
		return errs.New(
			errs.CodeUntrackedFilesPresent,
			"repository has untracked files",
			errs.WithDetails(details...),
			errs.WithHint("add, commit, or ignore untracked files, then retry"),
		)
	}

	if len(dirty) > 0 {
		details := []string{"Uncommitted changes:"}
		for _, line := range dirty {
			details = append(details, "  "+line)
		}
		return errs.New(
			errs.CodeWorktreeDirty,
			"repository has staged or unstaged changes",
			errs.WithDetails(details...),
			errs.WithHint("commit or discard local changes, then retry"),
		)
	}

	lfsSignals, err := detectLFSSignals(repoRoot)
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
			"Git LFS is not supported in procoder v1",
			errs.WithDetails(details...),
			errs.WithHint("remove Git LFS filters from attributes/config and retry"),
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
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "160000" {
			paths = append(paths, pathPart)
		}
	}
	return paths, nil
}

func detectLFSSignals(repoRoot string) ([]string, error) {
	var signals []string

	lfsConfigPath := filepath.Join(repoRoot, ".lfsconfig")
	if _, err := os.Stat(lfsConfigPath); err == nil {
		signals = append(signals, ".lfsconfig")
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != ".gitattributes" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(data)
		if strings.Contains(content, "filter=lfs") || strings.Contains(content, "diff=lfs") || strings.Contains(content, "merge=lfs") {
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				return relErr
			}
			signals = append(signals, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return signals, nil
}

func createTaskBranch(runner gitx.Runner, taskRef, headOID string) error {
	if _, err := runner.Run("update-ref", taskRef, headOID, ""); err != nil {
		return errs.Wrap(
			errs.CodeInternal,
			fmt.Sprintf("create task branch %s", taskRef),
			err,
			errs.WithHint("retry `procoder prepare` to create a new exchange"),
		)
	}
	return nil
}

func fetchContextRefs(exportRunner gitx.Runner, sourceRepo string) error {
	if _, err := exportRunner.Run("fetch", "--no-tags", sourceRepo, "+refs/heads/*:refs/heads/*"); err != nil {
		return errs.Wrap(errs.CodeInternal, "fetch local branches into export repository", err)
	}
	if _, err := exportRunner.Run("fetch", "--no-tags", sourceRepo, "+refs/tags/*:refs/tags/*"); err != nil {
		return errs.Wrap(errs.CodeInternal, "fetch local tags into export repository", err)
	}
	return nil
}

func initExportRepo(exportGit gitx.Runner) error {
	if _, err := exportGit.Run("init", "--initial-branch=procoder-export-empty"); err == nil {
		return nil
	}

	if _, err := exportGit.Run("init"); err != nil {
		return errs.Wrap(errs.CodeInternal, "initialize export repository", err)
	}
	if _, err := exportGit.Run("checkout", "--orphan", "procoder-export-empty"); err != nil {
		return errs.Wrap(errs.CodeInternal, "create export placeholder branch", err)
	}
	return nil
}

func stripGitState(gitDir string) error {
	for _, rel := range []string{"hooks", "logs", "refs/remotes"} {
		if err := os.RemoveAll(filepath.Join(gitDir, rel)); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(gitDir, "FETCH_HEAD")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func configureCommitIdentity(source, target gitx.Runner) error {
	userName, err := readGitConfigValue(source, "--local", "user.name")
	if err != nil {
		return errs.Wrap(errs.CodeInternal, "read source user.name", err)
	}
	if userName == "" {
		userName, err = readGitConfigValue(source, "--global", "user.name")
		if err != nil {
			return errs.Wrap(errs.CodeInternal, "read global user.name", err)
		}
	}
	if userName == "" {
		userName = defaultUserName
	}

	userEmail, err := readGitConfigValue(source, "--local", "user.email")
	if err != nil {
		return errs.Wrap(errs.CodeInternal, "read source user.email", err)
	}
	if userEmail == "" {
		userEmail, err = readGitConfigValue(source, "--global", "user.email")
		if err != nil {
			return errs.Wrap(errs.CodeInternal, "read global user.email", err)
		}
	}
	if userEmail == "" {
		userEmail = defaultUserEmail
	}

	if _, err := target.Run("config", "user.name", userName); err != nil {
		return errs.Wrap(errs.CodeInternal, "set export user.name", err)
	}
	if _, err := target.Run("config", "user.email", userEmail); err != nil {
		return errs.Wrap(errs.CodeInternal, "set export user.email", err)
	}
	return nil
}

func readGitConfigValue(runner gitx.Runner, scope, key string) (string, error) {
	result, err := runner.Run("config", scope, "--get", key)
	if err == nil {
		return strings.TrimSpace(result.Stdout), nil
	}
	if result.ExitCode == 1 {
		return "", nil
	}
	return "", err
}

func resolveHelperBinary(explicit string) (string, error) {
	envValue := os.Getenv(helperEnvVar)
	executablePath, _ := os.Executable()
	candidates := helperCandidatePaths(explicit, envValue, executablePath)
	if resolved, err := resolveHelperBinaryFromCandidates(candidates); err == nil {
		return resolved, nil
	}

	return "", errs.New(
		errs.CodeInternal,
		"procoder-return helper binary is not available",
		errs.WithHint("install or build procoder-return_linux_amd64, or set PROCODER_RETURN_HELPER to a prebuilt helper path and retry"),
	)
}

func helperCandidatePaths(explicit, envValue, executablePath string) []string {
	var candidates []string
	if v := strings.TrimSpace(explicit); v != "" {
		candidates = append(candidates, v)
	}
	if v := strings.TrimSpace(envValue); v != "" {
		candidates = append(candidates, v)
	}
	if v := strings.TrimSpace(executablePath); v != "" {
		exeDir := filepath.Dir(v)
		candidates = append(candidates,
			filepath.Join(exeDir, defaultHelperAsset),
			filepath.Join(exeDir, defaultHelperName),
		)
	}

	seen := make(map[string]struct{}, len(candidates))
	ordered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		ordered = append(ordered, candidate)
	}
	return ordered
}

func resolveHelperBinaryFromCandidates(candidates []string) (string, error) {
	for _, candidate := range candidates {
		if !isUsableHelperBinary(candidate) {
			continue
		}
		absPath, err := filepath.Abs(candidate)
		if err != nil {
			return "", err
		}
		return absPath, nil
	}
	return "", os.ErrNotExist
}

func isUsableHelperBinary(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return false
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return false
	}
	return true
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Chmod(0o755)
}

func appendExclude(gitDir string, patterns ...string) error {
	excludePath := filepath.Join(gitDir, "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return err
	}

	existing := make(map[string]struct{})
	if data, err := os.ReadFile(excludePath); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			existing[line] = struct{}{}
		}
		if err := scanner.Err(); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	f, err := os.OpenFile(excludePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if _, ok := existing[pattern]; ok {
			continue
		}
		if _, err := fmt.Fprintln(f, pattern); err != nil {
			return err
		}
	}
	return nil
}

func createZip(targetZip, sourceDir, rootPrefix string) error {
	file, err := os.Create(targetZip)
	if err != nil {
		return err
	}
	defer file.Close()

	zw := zip.NewWriter(file)
	defer zw.Close()

	return filepath.WalkDir(sourceDir, func(path string, d fs.DirEntry, walkErr error) error {
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

		zipPath := filepath.ToSlash(filepath.Join(rootPrefix, rel))
		info, err := d.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = zipPath
		if d.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()

		if _, err := io.Copy(writer, in); err != nil {
			return err
		}
		return nil
	})
}

func readRefSnapshot(runner gitx.Runner, namespace string) (map[string]string, error) {
	result, err := runner.Run("for-each-ref", "--format=%(refname) %(objectname)", namespace)
	if err != nil {
		return nil, err
	}

	refs := make(map[string]string)
	for _, line := range splitNonEmptyLines(result.Stdout) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		refs[fields[0]] = fields[1]
	}
	return refs, nil
}

func readTrimmed(runner gitx.Runner, args ...string) (string, error) {
	result, err := runner.Run(args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func splitNonEmptyLines(s string) []string {
	raw := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func isNotGitRepoError(err error) bool {
	typed, ok := errs.As(err)
	if !ok {
		return false
	}
	if typed.Code != errs.CodeGitCommandFailed {
		return false
	}

	var payload strings.Builder
	payload.WriteString(strings.ToLower(typed.Message))
	for _, detail := range typed.Details {
		payload.WriteByte('\n')
		payload.WriteString(strings.ToLower(detail))
	}

	all := payload.String()
	return strings.Contains(all, "not a git repository") ||
		strings.Contains(all, "must be run in a work tree")
}
