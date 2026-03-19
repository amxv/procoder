package apply

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/amxv/procoder/internal/errs"
	"github.com/amxv/procoder/internal/exchange"
	"github.com/amxv/procoder/internal/gitx"
)

const (
	returnJSONName        = "procoder-return.json"
	returnBundleName      = "procoder-return.bundle"
	importNamespacePrefix = "refs/procoder/import"
)

type Options struct {
	CWD               string
	ReturnPackagePath string
	Namespace         string
}

type Action string

const (
	ActionCreate   Action = "create"
	ActionUpdate   Action = "update"
	ActionConflict Action = "conflict"
)

type Check struct {
	Name   string
	Detail string
}

type PlanEntry struct {
	Action         Action
	SourceRef      string
	ImportedRef    string
	DestinationRef string
	OldOID         string
	NewOID         string
	CurrentOID     string
	Remapped       bool
	ConflictCode   errs.Code
}

type Summary struct {
	Creates   int
	Updates   int
	Conflicts int
}

type Plan struct {
	ExchangeID        string
	ReturnPackagePath string
	Namespace         string
	Checks            []Check
	Entries           []PlanEntry
	Summary           Summary
}

type importedUpdate struct {
	Update      exchange.RefUpdate
	ImportRef   string
	Destination string
}

func RunDryRun(opts Options) (plan Plan, runErr error) {
	cwd, err := resolveCWD(opts.CWD)
	if err != nil {
		return Plan{}, errs.Wrap(errs.CodeInternal, "resolve current working directory", err)
	}

	repoRoot, err := resolveRepo(cwd)
	if err != nil {
		return Plan{}, err
	}
	runner := gitx.NewRunner(repoRoot)

	returnPackagePath, err := resolveReturnPackagePath(cwd, opts.ReturnPackagePath)
	if err != nil {
		return Plan{}, err
	}

	namespacePrefix, err := normalizeNamespacePrefix(runner, opts.Namespace)
	if err != nil {
		return Plan{}, err
	}

	extractedDir, cleanupExtract, err := extractReturnPackage(returnPackagePath)
	if err != nil {
		return Plan{}, err
	}
	defer cleanupExtract()

	returnRecordPath := filepath.Join(extractedDir, returnJSONName)
	ret, err := readAndValidateReturnRecord(returnRecordPath)
	if err != nil {
		return Plan{}, err
	}

	bundlePath, err := resolveBundlePath(extractedDir, ret.BundleFile)
	if err != nil {
		return Plan{}, err
	}
	if err := verifyBundle(runner, bundlePath); err != nil {
		return Plan{}, err
	}

	importNonce, err := randomNonce()
	if err != nil {
		return Plan{}, errs.Wrap(errs.CodeInternal, "create temporary import namespace", err)
	}
	importPrefix := importNamespacePrefix + "/" + importNonce

	updates, err := buildImportedUpdates(ret, importPrefix, namespacePrefix)
	if err != nil {
		return Plan{}, err
	}
	if err := fetchBundleRefs(runner, bundlePath, updates); err != nil {
		return Plan{}, err
	}
	defer func() {
		cleanupErr := deleteImportRefs(runner, importPrefix)
		if cleanupErr != nil && runErr == nil {
			runErr = errs.Wrap(errs.CodeInternal, "clean temporary import refs", cleanupErr)
		}
	}()

	if err := verifyImportedTips(runner, importPrefix, updates); err != nil {
		return Plan{}, err
	}

	plan, err = buildPlan(runner, returnPackagePath, namespacePrefix, ret, updates)
	if err != nil {
		return Plan{}, err
	}

	plan.Checks = []Check{
		{Name: "return package structure", Detail: "found procoder-return.json and procoder-return.bundle"},
		{Name: "return record shape", Detail: "task-family refs match exchange and /task model"},
		{Name: "bundle verification", Detail: "git bundle verify passed"},
		{Name: "temporary import", Detail: "bundle refs fetched to internal namespace"},
		{Name: "fetched ref tips", Detail: "imported OIDs exactly match procoder-return.json"},
	}

	return plan, nil
}

func FormatDryRun(plan Plan) string {
	lines := []string{
		"Dry-run apply plan.",
		fmt.Sprintf("Exchange: %s", plan.ExchangeID),
		fmt.Sprintf("Return package: %s", plan.ReturnPackagePath),
		fmt.Sprintf("Namespace: %s", displayNamespace(plan.Namespace)),
		"",
		"Checks:",
	}
	for _, check := range plan.Checks {
		line := fmt.Sprintf("  OK  %s", check.Name)
		if strings.TrimSpace(check.Detail) != "" {
			line += " - " + check.Detail
		}
		lines = append(lines, line)
	}

	lines = append(lines, "", "Ref plan:")
	for _, entry := range plan.Entries {
		if entry.Remapped {
			lines = append(lines, fmt.Sprintf("  REMAP   %s -> %s", entry.SourceRef, entry.DestinationRef))
		}

		switch entry.Action {
		case ActionCreate:
			lines = append(lines, fmt.Sprintf("  CREATE  %s new=%s", entry.DestinationRef, entry.NewOID))
		case ActionUpdate:
			lines = append(lines, fmt.Sprintf("  UPDATE  %s old=%s new=%s", entry.DestinationRef, entry.OldOID, entry.NewOID))
		case ActionConflict:
			lines = append(lines, formatConflictLine(entry))
		default:
			lines = append(lines, fmt.Sprintf("  UNKNOWN %s", entry.DestinationRef))
		}
	}

	lines = append(lines,
		"",
		"Summary:",
		fmt.Sprintf("  creates: %d", plan.Summary.Creates),
		fmt.Sprintf("  updates: %d", plan.Summary.Updates),
		fmt.Sprintf("  conflicts: %d", plan.Summary.Conflicts),
	)
	if plan.Summary.Conflicts > 0 {
		if plan.Namespace == "" {
			lines = append(lines, "  result: conflicts detected; consider rerunning with --namespace <prefix>")
		} else {
			lines = append(lines, "  result: conflicts detected in namespace mapping")
		}
	} else {
		lines = append(lines, "  result: no conflicts detected")
	}
	lines = append(lines, "No refs were updated (dry-run).")

	return strings.Join(lines, "\n")
}

func formatConflictLine(entry PlanEntry) string {
	currentOID := displayOID(entry.CurrentOID)
	switch entry.ConflictCode {
	case errs.CodeBranchMoved:
		return fmt.Sprintf(
			"  CONFLICT BRANCH_MOVED %s expected-old=%s current=%s incoming=%s",
			entry.DestinationRef,
			displayOID(entry.OldOID),
			currentOID,
			displayOID(entry.NewOID),
		)
	case errs.CodeRefExists:
		return fmt.Sprintf(
			"  CONFLICT REF_EXISTS %s existing=%s incoming=%s",
			entry.DestinationRef,
			currentOID,
			displayOID(entry.NewOID),
		)
	default:
		return fmt.Sprintf("  CONFLICT %s %s", entry.ConflictCode, entry.DestinationRef)
	}
}

func resolveCWD(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return os.Getwd()
	}
	return cwd, nil
}

func resolveRepo(cwd string) (string, error) {
	root, err := readTrimmed(gitx.NewRunner(cwd), "rev-parse", "--show-toplevel")
	if err != nil {
		if isNotGitRepoError(err) {
			return "", errs.New(
				errs.CodeNotGitRepo,
				"current directory is not a Git worktree",
				errs.WithHint("run `procoder apply` inside a Git repository"),
			)
		}
		return "", errs.Wrap(errs.CodeInternal, "resolve repository root", err)
	}
	return root, nil
}

func resolveReturnPackagePath(cwd, packagePath string) (string, error) {
	packagePath = strings.TrimSpace(packagePath)
	if packagePath == "" {
		return "", errs.New(
			errs.CodeUnknownCommand,
			"missing return package path for `procoder apply`",
			errs.WithHint("run `procoder apply --help`"),
		)
	}

	if !filepath.IsAbs(packagePath) {
		packagePath = filepath.Join(cwd, packagePath)
	}
	absPath, err := filepath.Abs(packagePath)
	if err != nil {
		return "", errs.Wrap(errs.CodeInternal, "resolve return package path", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errs.New(
				errs.CodeInvalidReturnPackage,
				"return package path does not exist",
				errs.WithDetails("Path: "+absPath),
			)
		}
		return "", errs.Wrap(errs.CodeInternal, "stat return package path", err)
	}
	if info.IsDir() {
		return "", errs.New(
			errs.CodeInvalidReturnPackage,
			"return package path must be a zip file",
			errs.WithDetails("Path: "+absPath),
		)
	}
	return absPath, nil
}

func normalizeNamespacePrefix(runner gitx.Runner, namespace string) (string, error) {
	raw := strings.TrimSpace(namespace)
	if raw == "" {
		return "", nil
	}

	prefix := strings.TrimPrefix(raw, "refs/heads/")
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return "", errs.New(
			errs.CodeUnknownCommand,
			fmt.Sprintf("invalid namespace prefix %q", raw),
			errs.WithHint("use a Git-valid namespace prefix, for example `procoder-import`"),
		)
	}

	candidate := "refs/heads/" + prefix + "/task"
	if _, err := runner.Run("check-ref-format", candidate); err != nil {
		return "", errs.New(
			errs.CodeUnknownCommand,
			fmt.Sprintf("invalid namespace prefix %q", raw),
			errs.WithHint("use a Git-valid namespace prefix, for example `procoder-import`"),
		)
	}

	return prefix, nil
}

func extractReturnPackage(returnPackagePath string) (string, func(), error) {
	reader, err := zip.OpenReader(returnPackagePath)
	if err != nil {
		return "", nil, errs.New(
			errs.CodeInvalidReturnPackage,
			"return package is not a valid zip archive",
			errs.WithDetails("Path: "+returnPackagePath),
		)
	}
	defer reader.Close()

	destDir, err := os.MkdirTemp("", "procoder-apply-*")
	if err != nil {
		return "", nil, errs.Wrap(errs.CodeInternal, "create temporary return package directory", err)
	}

	cleanup := func() {
		_ = os.RemoveAll(destDir)
	}

	for _, file := range reader.File {
		if err := extractZipEntry(destDir, file); err != nil {
			cleanup()
			return "", nil, err
		}
	}

	if _, err := os.Stat(filepath.Join(destDir, returnJSONName)); err != nil {
		cleanup()
		if os.IsNotExist(err) {
			return "", nil, errs.New(
				errs.CodeInvalidReturnPackage,
				"missing procoder-return.json in return package",
				errs.WithDetails("Path: "+returnPackagePath),
			)
		}
		return "", nil, errs.Wrap(errs.CodeInternal, "inspect extracted return metadata", err)
	}

	return destDir, cleanup, nil
}

func extractZipEntry(destDir string, file *zip.File) error {
	cleanName := filepath.Clean(file.Name)
	if strings.HasPrefix(cleanName, "../") || cleanName == ".." || filepath.IsAbs(file.Name) {
		return errs.New(
			errs.CodeInvalidReturnPackage,
			"return package contains an invalid zip entry path",
			errs.WithDetails("Entry: "+file.Name),
		)
	}

	targetPath := filepath.Join(destDir, cleanName)
	safePrefix := filepath.Clean(destDir) + string(os.PathSeparator)
	if !strings.HasPrefix(filepath.Clean(targetPath), safePrefix) {
		return errs.New(
			errs.CodeInvalidReturnPackage,
			"return package contains an invalid zip entry path",
			errs.WithDetails("Entry: "+file.Name),
		)
	}

	if file.FileInfo().IsDir() {
		if err := os.MkdirAll(targetPath, 0o755); err != nil {
			return errs.Wrap(errs.CodeInternal, "create extracted return package directory", err)
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return errs.Wrap(errs.CodeInternal, "create extracted return package directory", err)
	}

	in, err := file.Open()
	if err != nil {
		return errs.Wrap(errs.CodeInternal, "open zip entry", err)
	}
	defer in.Close()

	mode := file.Mode()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, "create extracted file", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return errs.Wrap(errs.CodeInternal, "copy extracted zip entry", err)
	}
	return nil
}

func readAndValidateReturnRecord(returnRecordPath string) (exchange.Return, error) {
	ret, err := exchange.ReadReturn(returnRecordPath)
	if err != nil {
		return exchange.Return{}, errs.New(
			errs.CodeInvalidReturnPackage,
			"missing or invalid procoder-return.json",
			errs.WithDetails("Path: "+returnRecordPath),
		)
	}
	if err := validateReturnRecord(ret); err != nil {
		return exchange.Return{}, err
	}
	return ret, nil
}

func validateReturnRecord(ret exchange.Return) error {
	if strings.TrimSpace(ret.Protocol) != exchange.ReturnProtocolV1 {
		return invalidReturn("procoder-return.json has an unsupported protocol")
	}
	if exchange.TaskRootRef(ret.ExchangeID) == "" {
		return invalidReturn("procoder-return.json has an invalid exchange_id")
	}
	if ret.Task.RootRef != exchange.TaskRootRef(ret.ExchangeID) {
		return invalidReturn("procoder-return.json task.root_ref does not match exchange_id /task model")
	}
	if strings.TrimSpace(ret.Task.BaseOID) == "" {
		return invalidReturn("procoder-return.json task.base_oid is required")
	}
	if strings.TrimSpace(ret.BundleFile) == "" {
		return invalidReturn("procoder-return.json bundle_file is required")
	}
	if filepath.Base(ret.BundleFile) != ret.BundleFile {
		return invalidReturn("procoder-return.json bundle_file must be a file name")
	}
	if ret.BundleFile != returnBundleName {
		return invalidReturn(fmt.Sprintf("procoder-return.json bundle_file must be %q", returnBundleName))
	}
	if len(ret.Updates) == 0 {
		return invalidReturn("procoder-return.json updates must not be empty")
	}

	taskPrefix := exchange.TaskRefPrefix(ret.ExchangeID)
	seen := make(map[string]struct{}, len(ret.Updates))
	for _, update := range ret.Updates {
		if strings.TrimSpace(update.Ref) == "" {
			return invalidReturn("procoder-return.json includes an update with an empty ref")
		}
		if !exchange.IsTaskRef(ret.ExchangeID, update.Ref) || !strings.HasPrefix(update.Ref, taskPrefix+"/") {
			return invalidReturn("procoder-return.json includes a ref outside the allowed task branch family")
		}
		if strings.TrimSpace(update.NewOID) == "" {
			return invalidReturn(fmt.Sprintf("procoder-return.json update for %s is missing new_oid", update.Ref))
		}
		if _, exists := seen[update.Ref]; exists {
			return invalidReturn(fmt.Sprintf("procoder-return.json contains duplicate ref updates for %s", update.Ref))
		}
		seen[update.Ref] = struct{}{}
	}

	return nil
}

func invalidReturn(problem string) error {
	return errs.New(errs.CodeInvalidReturnPackage, problem)
}

func resolveBundlePath(extractedDir, bundleFile string) (string, error) {
	bundlePath := filepath.Join(extractedDir, bundleFile)
	if _, err := os.Stat(bundlePath); err != nil {
		if os.IsNotExist(err) {
			return "", errs.New(
				errs.CodeInvalidReturnPackage,
				"return package is missing procoder-return.bundle",
				errs.WithDetails("Path: "+bundlePath),
			)
		}
		return "", errs.Wrap(errs.CodeInternal, "inspect extracted return bundle", err)
	}
	return bundlePath, nil
}

func verifyBundle(runner gitx.Runner, bundlePath string) error {
	result, err := runner.Run("bundle", "verify", bundlePath)
	if err == nil {
		return nil
	}

	details := []string{"Bundle: " + bundlePath}
	if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
		details = append(details, "Git output: "+stderr)
	}
	return errs.New(
		errs.CodeBundleVerifyFailed,
		"return bundle verification failed",
		errs.WithDetails(details...),
		errs.WithHint("ensure this return package matches the local exchange repository and retry"),
	)
}

func randomNonce() (string, error) {
	buf := make([]byte, 4)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func buildImportedUpdates(ret exchange.Return, importPrefix, namespacePrefix string) ([]importedUpdate, error) {
	updates := make([]exchange.RefUpdate, len(ret.Updates))
	copy(updates, ret.Updates)
	sort.Slice(updates, func(i, j int) bool { return updates[i].Ref < updates[j].Ref })

	mappings := make([]importedUpdate, 0, len(updates))
	for _, update := range updates {
		destination, err := destinationRef(update.Ref, ret.ExchangeID, namespacePrefix)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, importedUpdate{
			Update:      update,
			ImportRef:   importPrefix + "/" + strings.TrimPrefix(update.Ref, "refs/"),
			Destination: destination,
		})
	}
	return mappings, nil
}

func destinationRef(sourceRef, exchangeID, namespacePrefix string) (string, error) {
	if namespacePrefix == "" {
		return sourceRef, nil
	}

	taskPrefix := exchange.TaskRefPrefix(exchangeID)
	if !strings.HasPrefix(sourceRef, taskPrefix+"/") {
		return "", errs.New(
			errs.CodeInvalidReturnPackage,
			"return package includes a ref outside the allowed task branch family",
			errs.WithDetails("Ref: "+sourceRef),
		)
	}
	suffix := strings.TrimPrefix(sourceRef, taskPrefix)
	return "refs/heads/" + namespacePrefix + "/" + exchangeID + suffix, nil
}

func fetchBundleRefs(runner gitx.Runner, bundlePath string, updates []importedUpdate) error {
	args := []string{"fetch", "--no-tags", bundlePath}
	for _, update := range updates {
		args = append(args, fmt.Sprintf("+%s:%s", update.Update.Ref, update.ImportRef))
	}

	if _, err := runner.Run(args...); err != nil {
		return errs.New(
			errs.CodeInvalidReturnPackage,
			"failed to fetch expected refs from procoder-return.bundle",
			errs.WithDetails("Bundle: "+bundlePath),
		)
	}
	return nil
}

func verifyImportedTips(runner gitx.Runner, importPrefix string, updates []importedUpdate) error {
	fetched, err := readRefSnapshot(runner, importPrefix)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, "read imported refs", err)
	}

	if len(fetched) != len(updates) {
		return errs.New(
			errs.CodeInvalidReturnPackage,
			"fetched ref set does not match procoder-return.json",
			errs.WithDetails(
				fmt.Sprintf("Expected refs: %d", len(updates)),
				fmt.Sprintf("Fetched refs: %d", len(fetched)),
			),
		)
	}

	for _, update := range updates {
		gotOID, ok := fetched[update.ImportRef]
		if !ok {
			return errs.New(
				errs.CodeInvalidReturnPackage,
				"fetched refs do not match procoder-return.json",
				errs.WithDetails("Missing import ref: "+update.ImportRef),
			)
		}
		if gotOID != update.Update.NewOID {
			return errs.New(
				errs.CodeInvalidReturnPackage,
				"fetched ref tip does not match procoder-return.json",
				errs.WithDetails(
					"Ref: "+update.Update.Ref,
					"Expected OID: "+update.Update.NewOID,
					"Fetched OID: "+gotOID,
				),
			)
		}
	}

	return nil
}

func buildPlan(runner gitx.Runner, returnPackagePath, namespacePrefix string, ret exchange.Return, updates []importedUpdate) (Plan, error) {
	plan := Plan{
		ExchangeID:        ret.ExchangeID,
		ReturnPackagePath: returnPackagePath,
		Namespace:         namespacePrefix,
		Entries:           make([]PlanEntry, 0, len(updates)),
	}

	for _, update := range updates {
		currentOID, exists, err := resolveRefOID(runner, update.Destination)
		if err != nil {
			return Plan{}, err
		}

		entry := PlanEntry{
			SourceRef:      update.Update.Ref,
			ImportedRef:    update.ImportRef,
			DestinationRef: update.Destination,
			OldOID:         update.Update.OldOID,
			NewOID:         update.Update.NewOID,
			CurrentOID:     currentOID,
			Remapped:       update.Destination != update.Update.Ref,
		}

		if strings.TrimSpace(update.Update.OldOID) == "" {
			if exists {
				entry.Action = ActionConflict
				entry.ConflictCode = errs.CodeRefExists
				plan.Summary.Conflicts++
			} else {
				entry.Action = ActionCreate
				plan.Summary.Creates++
			}
		} else {
			if !exists || currentOID != update.Update.OldOID {
				entry.Action = ActionConflict
				entry.ConflictCode = errs.CodeBranchMoved
				plan.Summary.Conflicts++
			} else {
				entry.Action = ActionUpdate
				plan.Summary.Updates++
			}
		}

		plan.Entries = append(plan.Entries, entry)
	}

	return plan, nil
}

func resolveRefOID(runner gitx.Runner, ref string) (oid string, exists bool, err error) {
	result, runErr := runner.Run("for-each-ref", "--format=%(refname) %(objectname)", ref)
	if runErr != nil {
		return "", false, errs.Wrap(errs.CodeInternal, fmt.Sprintf("resolve local ref %s", ref), runErr)
	}

	for _, line := range splitNonEmptyLines(result.Stdout) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[0] == ref {
			return fields[1], true, nil
		}
	}
	return "", false, nil
}

func deleteImportRefs(runner gitx.Runner, importPrefix string) error {
	refs, err := readRefNames(runner, importPrefix)
	if err != nil {
		return err
	}

	for _, ref := range refs {
		if _, err := runner.Run("update-ref", "-d", ref); err != nil {
			return err
		}
	}
	return nil
}

func readRefNames(runner gitx.Runner, namespace string) ([]string, error) {
	result, err := runner.Run("for-each-ref", "--format=%(refname)", namespace)
	if err != nil {
		return nil, errs.Wrap(errs.CodeInternal, "read refs", err)
	}

	lines := splitNonEmptyLines(result.Stdout)
	sort.Strings(lines)
	return lines, nil
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

func displayOID(oid string) string {
	if strings.TrimSpace(oid) == "" {
		return "(none)"
	}
	return oid
}

func displayNamespace(namespace string) string {
	if strings.TrimSpace(namespace) == "" {
		return "(default task-family refs)"
	}
	return "refs/heads/" + namespace + "/<exchange-id>/..."
}
