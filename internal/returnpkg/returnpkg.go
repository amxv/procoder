package returnpkg

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/amxv/procoder/internal/errs"
	"github.com/amxv/procoder/internal/exchange"
	"github.com/amxv/procoder/internal/gitx"
)

const (
	defaultToolVersion = "dev"
	exchangeRelPath    = ".git/procoder/exchange.json"
	returnJSONName     = "procoder-return.json"
	returnBundleName   = "procoder-return.bundle"
	returnZipNameFmt   = "procoder-return-%s.zip"
	successIntro       = "Created return package."
)

type Options struct {
	CWD         string
	ToolVersion string
	Now         func() time.Time
}

type Result struct {
	ExchangeID        string
	ReturnPackagePath string
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
	runner := gitx.NewRunner(repoRoot)

	exchangePath := filepath.Join(gitDir, "procoder", "exchange.json")
	ex, err := readExchange(exchangePath)
	if err != nil {
		return Result{}, err
	}

	if err := validateCleanWorktree(runner); err != nil {
		return Result{}, err
	}

	currentHeads, err := readRefSnapshot(runner, "refs/heads")
	if err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "read current branch refs", err)
	}
	currentTags, err := readRefSnapshot(runner, "refs/tags")
	if err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "read current tag refs", err)
	}

	taskUpdates, outOfScopeHeadChanges, err := compareHeadChanges(ex, currentHeads)
	if err != nil {
		return Result{}, err
	}
	tagChanges := diffRefChanges(ex.Context.Tags, currentTags)

	if len(outOfScopeHeadChanges) > 0 {
		return Result{}, errs.New(
			errs.CodeRefOutOfScope,
			"changed refs are outside the allowed task branch family",
			errs.WithDetails(renderRefScopeDetails(outOfScopeHeadChanges, ex.Task.RefPrefix)...),
			errs.WithHint("move your commits onto the task branch family, then rerun ./procoder-return"),
		)
	}

	if len(tagChanges) > 0 {
		return Result{}, errs.New(
			errs.CodeRefOutOfScope,
			"tag changes are not supported in procoder v1 return packages",
			errs.WithDetails(renderTagChangeDetails(tagChanges)...),
			errs.WithHint("remove tag changes, then rerun ./procoder-return"),
		)
	}

	if len(taskUpdates) == 0 {
		return Result{}, errs.New(
			errs.CodeNoNewCommits,
			"no new commits found in the task branch family",
			errs.WithDetails(fmt.Sprintf("Task branch family: %s/*", ex.Task.RefPrefix)),
			errs.WithHint("create at least one commit on the task branch family, then rerun ./procoder-return"),
		)
	}

	if err := validateDescendants(runner, ex, taskUpdates); err != nil {
		return Result{}, err
	}

	toolVersion := strings.TrimSpace(opts.ToolVersion)
	if toolVersion == "" {
		toolVersion = defaultToolVersion
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}

	ret := exchange.Return{
		Protocol:    exchange.ReturnProtocolV1,
		ExchangeID:  ex.ExchangeID,
		CreatedAt:   nowFn().UTC(),
		ToolVersion: toolVersion,
		BundleFile:  returnBundleName,
		Task: exchange.ReturnTask{
			RootRef: ex.Task.RootRef,
			BaseOID: ex.Task.BaseOID,
		},
		Updates: taskUpdates,
	}

	tempDir, err := os.MkdirTemp("", "procoder-return-*")
	if err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "create temporary return package directory", err)
	}
	defer os.RemoveAll(tempDir)

	returnJSONPath := filepath.Join(tempDir, returnJSONName)
	if err := exchange.WriteReturn(returnJSONPath, ret); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "write return package metadata", err)
	}

	bundlePath := filepath.Join(tempDir, returnBundleName)
	if err := createBundle(runner, bundlePath, ex.Task.BaseOID, taskUpdates); err != nil {
		return Result{}, err
	}

	returnPackageName := fmt.Sprintf(returnZipNameFmt, ex.ExchangeID)
	returnPackagePath := filepath.Join(repoRoot, returnPackageName)
	if err := createZip(returnPackagePath, map[string]string{
		returnJSONName:   returnJSONPath,
		returnBundleName: bundlePath,
	}); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "create return package zip", err)
	}

	if err := appendExclude(gitDir, returnPackageName); err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "update .git/info/exclude with return package", err)
	}

	absPath, err := filepath.Abs(returnPackagePath)
	if err != nil {
		return Result{}, errs.Wrap(errs.CodeInternal, "resolve absolute return package path", err)
	}

	return Result{
		ExchangeID:        ex.ExchangeID,
		ReturnPackagePath: absPath,
	}, nil
}

func FormatSuccess(result Result) string {
	sandboxPath := "sandbox:" + filepath.ToSlash(result.ReturnPackagePath)
	return strings.Join([]string{
		successIntro,
		fmt.Sprintf("Return package: %s", result.ReturnPackagePath),
		fmt.Sprintf("Sandbox hint: %s", sandboxPath),
	}, "\n")
}

type refChange struct {
	Ref    string
	OldOID string
	NewOID string
}

func compareHeadChanges(ex exchange.Exchange, currentHeads map[string]string) ([]exchange.RefUpdate, []refChange, error) {
	baselineHeads := map[string]string{}
	for ref, oid := range ex.Context.Heads {
		baselineHeads[ref] = oid
	}
	if _, ok := baselineHeads[ex.Task.RootRef]; !ok {
		baselineHeads[ex.Task.RootRef] = ex.Task.BaseOID
	}

	changes := diffRefChanges(baselineHeads, currentHeads)
	taskUpdates := make([]exchange.RefUpdate, 0, len(changes))
	outOfScope := make([]refChange, 0)

	for _, change := range changes {
		if !exchange.IsTaskRef(ex.ExchangeID, change.Ref) {
			outOfScope = append(outOfScope, change)
			continue
		}
		if change.NewOID == "" {
			return nil, nil, errs.New(
				errs.CodeRefOutOfScope,
				"task branch deletions are not supported in procoder v1 return packages",
				errs.WithDetails(
					fmt.Sprintf("Deleted ref: %s", change.Ref),
					fmt.Sprintf("Task branch family: %s/*", ex.Task.RefPrefix),
				),
				errs.WithHint("restore the deleted task branch and rerun ./procoder-return"),
			)
		}
		taskUpdates = append(taskUpdates, exchange.RefUpdate{
			Ref:    change.Ref,
			OldOID: change.OldOID,
			NewOID: change.NewOID,
		})
	}

	sort.Slice(taskUpdates, func(i, j int) bool { return taskUpdates[i].Ref < taskUpdates[j].Ref })
	sort.Slice(outOfScope, func(i, j int) bool { return outOfScope[i].Ref < outOfScope[j].Ref })
	return taskUpdates, outOfScope, nil
}

func validateDescendants(runner gitx.Runner, ex exchange.Exchange, updates []exchange.RefUpdate) error {
	for _, update := range updates {
		result, err := runner.Run("merge-base", "--is-ancestor", ex.Task.BaseOID, update.NewOID)
		if err == nil {
			continue
		}
		if result.ExitCode == 1 {
			return errs.New(
				errs.CodeRefOutOfScope,
				"changed task ref does not descend from the task base commit",
				errs.WithDetails(
					fmt.Sprintf("Ref: %s", update.Ref),
					fmt.Sprintf("Task base OID: %s", ex.Task.BaseOID),
					fmt.Sprintf("Ref OID: %s", update.NewOID),
				),
				errs.WithHint("move this ref to a descendant of the task base commit, then rerun ./procoder-return"),
			)
		}
		return errs.Wrap(errs.CodeInternal, fmt.Sprintf("validate task ref ancestry for %s", update.Ref), err)
	}
	return nil
}

func createBundle(runner gitx.Runner, bundlePath, baseOID string, updates []exchange.RefUpdate) error {
	args := []string{"bundle", "create", bundlePath}
	for _, update := range updates {
		args = append(args, fmt.Sprintf("%s..%s", baseOID, update.Ref))
	}

	if _, err := runner.Run(args...); err != nil {
		return errs.Wrap(errs.CodeInternal, "create procoder return bundle", err)
	}
	return nil
}

func createZip(target string, files map[string]string) error {
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		src := files[name]
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = name
		header.Method = zip.Deflate

		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}

		in, err := os.Open(src)
		if err != nil {
			return err
		}
		if _, err := io.Copy(writer, in); err != nil {
			_ = in.Close()
			return err
		}
		_ = in.Close()
	}

	return nil
}

func validateCleanWorktree(runner gitx.Runner) error {
	result, err := runner.Run("status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return errs.Wrap(errs.CodeInternal, "read repository status", err)
	}
	lines := splitNonEmptyLines(result.Stdout)
	if len(lines) == 0 {
		return nil
	}

	details := []string{"Found:"}
	for _, line := range lines {
		details = append(details, "  "+line)
	}
	return errs.New(
		errs.CodeWorktreeDirty,
		"repository has uncommitted or untracked changes",
		errs.WithDetails(details...),
		errs.WithHint("commit or discard these changes, then rerun ./procoder-return"),
	)
}

func readExchange(path string) (exchange.Exchange, error) {
	ex, err := exchange.ReadExchange(path)
	if err != nil {
		return exchange.Exchange{}, errs.New(
			errs.CodeInvalidExchange,
			"missing or invalid exchange metadata",
			errs.WithDetails(
				fmt.Sprintf("Path: %s", exchangeRelPath),
			),
			errs.WithHint("run ./procoder-return only inside a repository created by procoder prepare"),
		)
	}

	if err := validateExchange(ex); err != nil {
		return exchange.Exchange{}, err
	}
	return ex, nil
}

func validateExchange(ex exchange.Exchange) error {
	if ex.ExchangeID == "" {
		return invalidExchange("exchange id is missing")
	}
	if ex.Task.RootRef == "" || ex.Task.BaseOID == "" || ex.Task.RefPrefix == "" {
		return invalidExchange("task metadata is incomplete")
	}
	if ex.Task.RootRef != exchange.TaskRootRef(ex.ExchangeID) {
		return invalidExchange("task root ref does not match exchange id")
	}
	expectedPrefix := exchange.TaskRefPrefix(ex.ExchangeID)
	if ex.Task.RefPrefix != expectedPrefix {
		return invalidExchange("task ref prefix does not match exchange id")
	}
	if !exchange.IsTaskRef(ex.ExchangeID, ex.Task.RootRef) {
		return invalidExchange("task root ref is outside the task branch family")
	}
	return nil
}

func invalidExchange(problem string) error {
	return errs.New(
		errs.CodeInvalidExchange,
		problem,
		errs.WithDetails(fmt.Sprintf("Path: %s", exchangeRelPath)),
		errs.WithHint("run ./procoder-return only inside a repository created by procoder prepare"),
	)
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
				errs.WithHint("run ./procoder-return inside a prepared task package repository"),
			)
		}
		return "", "", errs.Wrap(errs.CodeInternal, "resolve repository root", err)
	}

	gitDirRaw, err := readTrimmed(gitx.NewRunner(root), "rev-parse", "--git-dir")
	if err != nil {
		return "", "", errs.Wrap(errs.CodeInternal, "resolve repository git directory", err)
	}

	gitDir := gitDirRaw
	if !filepath.IsAbs(gitDirRaw) {
		gitDir = filepath.Join(root, gitDirRaw)
	}
	return root, filepath.Clean(gitDir), nil
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

func diffRefChanges(baseline, current map[string]string) []refChange {
	normalizedBaseline := baseline
	if normalizedBaseline == nil {
		normalizedBaseline = map[string]string{}
	}
	normalizedCurrent := current
	if normalizedCurrent == nil {
		normalizedCurrent = map[string]string{}
	}

	keySet := map[string]struct{}{}
	for ref := range normalizedBaseline {
		keySet[ref] = struct{}{}
	}
	for ref := range normalizedCurrent {
		keySet[ref] = struct{}{}
	}

	refs := make([]string, 0, len(keySet))
	for ref := range keySet {
		refs = append(refs, ref)
	}
	sort.Strings(refs)

	changes := make([]refChange, 0, len(refs))
	for _, ref := range refs {
		oldOID, oldOK := normalizedBaseline[ref]
		newOID, newOK := normalizedCurrent[ref]

		if ref == "" {
			continue
		}
		if !oldOK {
			oldOID = ""
		}
		if !newOK {
			newOID = ""
		}

		if oldOID == newOID {
			continue
		}
		changes = append(changes, refChange{
			Ref:    ref,
			OldOID: oldOID,
			NewOID: newOID,
		})
	}
	return changes
}

func renderRefScopeDetails(changes []refChange, taskPrefix string) []string {
	details := []string{"Changed refs:"}
	for _, change := range changes {
		details = append(details, fmt.Sprintf("  %s (%s -> %s)", change.Ref, displayOID(change.OldOID), displayOID(change.NewOID)))
	}
	details = append(details,
		"Allowed refs:",
		fmt.Sprintf("  %s/*", taskPrefix),
	)
	return details
}

func renderTagChangeDetails(changes []refChange) []string {
	details := []string{"Changed tags:"}
	for _, change := range changes {
		details = append(details, fmt.Sprintf("  %s (%s -> %s)", change.Ref, displayOID(change.OldOID), displayOID(change.NewOID)))
	}
	return details
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

func displayOID(oid string) string {
	if strings.TrimSpace(oid) == "" {
		return "(none)"
	}
	return oid
}
