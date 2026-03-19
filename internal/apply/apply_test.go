package apply

import (
	"archive/zip"
	"bytes"
	"encoding/json"
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
	"github.com/amxv/procoder/internal/returnpkg"
	"github.com/amxv/procoder/internal/testutil/gitrepo"
)

func TestRunDryRunHappyPathWithRealReturnPackage(t *testing.T) {
	fixture := setupReturnFixture(t, mutateTaskRootCommit)

	plan, err := RunDryRun(Options{
		CWD:               fixture.sourceRepo.Dir,
		ReturnPackagePath: fixture.returnPackagePath,
	})
	if err != nil {
		t.Fatalf("RunDryRun returned error: %v", err)
	}

	if plan.ExchangeID != fixture.exchange.ExchangeID {
		t.Fatalf("unexpected exchange id: got %q want %q", plan.ExchangeID, fixture.exchange.ExchangeID)
	}
	if plan.Summary.Updates != 1 || plan.Summary.Creates != 0 || plan.Summary.Conflicts != 0 {
		t.Fatalf("unexpected summary: %#v", plan.Summary)
	}

	entry, ok := findEntry(plan.Entries, fixture.exchange.Task.RootRef)
	if !ok {
		t.Fatalf("expected task root entry in plan: %#v", plan.Entries)
	}
	if entry.Action != ActionUpdate {
		t.Fatalf("expected update action, got %s", entry.Action)
	}
	if entry.OldOID != fixture.returnRecord.Updates[0].OldOID {
		t.Fatalf("unexpected old oid: got %q want %q", entry.OldOID, fixture.returnRecord.Updates[0].OldOID)
	}
	if entry.NewOID != fixture.returnRecord.Updates[0].NewOID {
		t.Fatalf("unexpected new oid: got %q want %q", entry.NewOID, fixture.returnRecord.Updates[0].NewOID)
	}

	formatted := FormatDryRun(plan)
	for _, fragment := range []string{
		"Dry-run apply plan.",
		"Checks:",
		"Ref plan:",
		"Summary:",
		"UPDATE",
		"No refs were updated (dry-run).",
	} {
		if !strings.Contains(formatted, fragment) {
			t.Fatalf("expected formatted plan to contain %q, got:\n%s", fragment, formatted)
		}
	}

	importRefs := strings.TrimSpace(runGit(t, fixture.sourceRepo.Dir, "for-each-ref", "--format=%(refname)", "refs/procoder/import"))
	if importRefs != "" {
		t.Fatalf("expected temporary import refs to be cleaned up, got:\n%s", importRefs)
	}
}

func TestRunDryRunFailsInvalidReturnPackage(t *testing.T) {
	repo := gitrepo.New(t)
	invalidPath := filepath.Join(t.TempDir(), "broken.zip")
	if err := os.WriteFile(invalidPath, []byte("not-a-zip"), 0o644); err != nil {
		t.Fatalf("write invalid zip failed: %v", err)
	}

	_, err := RunDryRun(Options{
		CWD:               repo.Dir,
		ReturnPackagePath: invalidPath,
	})
	assertErrorContains(t, err, errs.CodeInvalidReturnPackage, "not a valid zip archive")
}

func TestRunDryRunFailsInvalidJSON(t *testing.T) {
	repo := gitrepo.New(t)
	invalidZip := filepath.Join(t.TempDir(), "invalid-json.zip")
	writeZip(t, invalidZip, map[string][]byte{
		returnJSONName:   []byte("{invalid json\n"),
		returnBundleName: []byte("bundle placeholder"),
	})

	_, err := RunDryRun(Options{
		CWD:               repo.Dir,
		ReturnPackagePath: invalidZip,
	})
	assertErrorContains(t, err, errs.CodeInvalidReturnPackage, "missing or invalid procoder-return.json")
}

func TestRunDryRunFailsBundleVerification(t *testing.T) {
	fixture := setupReturnFixture(t, mutateTaskRootCommit)

	tamperedPath := rewriteReturnPackage(t, fixture.returnPackagePath, func(files map[string][]byte) {
		files[returnBundleName] = []byte("this is not a git bundle")
	})

	_, err := RunDryRun(Options{
		CWD:               fixture.sourceRepo.Dir,
		ReturnPackagePath: tamperedPath,
	})
	assertErrorContains(t, err, errs.CodeBundleVerifyFailed, "return bundle verification failed")
}

func TestRunDryRunFailsMismatchedFetchedOID(t *testing.T) {
	fixture := setupReturnFixture(t, mutateTaskRootCommit)

	tamperedPath := rewriteReturnPackage(t, fixture.returnPackagePath, func(files map[string][]byte) {
		var ret exchange.Return
		if err := json.Unmarshal(files[returnJSONName], &ret); err != nil {
			t.Fatalf("decode return json failed: %v", err)
		}
		if len(ret.Updates) == 0 {
			t.Fatalf("expected updates in return json")
		}
		ret.Updates[0].NewOID = strings.Repeat("f", 40)
		encoded, err := json.MarshalIndent(ret, "", "  ")
		if err != nil {
			t.Fatalf("encode return json failed: %v", err)
		}
		files[returnJSONName] = append(encoded, '\n')
	})

	_, err := RunDryRun(Options{
		CWD:               fixture.sourceRepo.Dir,
		ReturnPackagePath: tamperedPath,
	})
	assertErrorContains(t, err, errs.CodeInvalidReturnPackage, "fetched ref tip does not match procoder-return.json")
}

func TestRunDryRunNamespaceMappingInPlan(t *testing.T) {
	fixture := setupReturnFixture(t, mutateSiblingTaskBranchCommit)

	const namespace = "procoder-import"
	plan, err := RunDryRun(Options{
		CWD:               fixture.sourceRepo.Dir,
		ReturnPackagePath: fixture.returnPackagePath,
		Namespace:         namespace,
	})
	if err != nil {
		t.Fatalf("RunDryRun returned error: %v", err)
	}

	if len(plan.Entries) == 0 {
		t.Fatalf("expected plan entries")
	}
	for _, entry := range plan.Entries {
		if !entry.Remapped {
			t.Fatalf("expected remapped entry in namespace mode: %#v", entry)
		}
		wantPrefix := "refs/heads/" + namespace + "/" + fixture.exchange.ExchangeID + "/"
		if !strings.HasPrefix(entry.DestinationRef, wantPrefix) {
			t.Fatalf("unexpected destination ref: got %q want prefix %q", entry.DestinationRef, wantPrefix)
		}
	}

	formatted := FormatDryRun(plan)
	if !strings.Contains(formatted, "REMAP") {
		t.Fatalf("expected remap lines in dry-run output, got:\n%s", formatted)
	}
}

func TestRunDryRunDetectsBranchMovedConflict(t *testing.T) {
	fixture := setupReturnFixture(t, mutateTaskRootCommit)

	fixture.sourceRepo.WriteFile("local-main.txt", "local branch moved\n")
	localHead := strings.TrimSpace(fixture.sourceRepo.CommitAll("local branch movement"))
	fixture.sourceRepo.Git("update-ref", fixture.exchange.Task.RootRef, localHead)

	plan, err := RunDryRun(Options{
		CWD:               fixture.sourceRepo.Dir,
		ReturnPackagePath: fixture.returnPackagePath,
	})
	if err != nil {
		t.Fatalf("RunDryRun returned error: %v", err)
	}

	entry, ok := findEntry(plan.Entries, fixture.exchange.Task.RootRef)
	if !ok {
		t.Fatalf("expected task root entry in plan")
	}
	if entry.Action != ActionConflict {
		t.Fatalf("expected conflict action, got %s", entry.Action)
	}
	if entry.ConflictCode != errs.CodeBranchMoved {
		t.Fatalf("expected BRANCH_MOVED conflict, got %s", entry.ConflictCode)
	}
	if plan.Summary.Conflicts == 0 {
		t.Fatalf("expected conflict count > 0")
	}

	formatted := FormatDryRun(plan)
	if !strings.Contains(formatted, "CONFLICT BRANCH_MOVED") {
		t.Fatalf("expected branch moved line in output, got:\n%s", formatted)
	}
}

func TestRunDryRunDetectsNamespaceRefExistsConflict(t *testing.T) {
	fixture := setupReturnFixture(t, mutateSiblingTaskBranchCommit)

	const namespace = "procoder-import"
	if len(fixture.returnRecord.Updates) == 0 {
		t.Fatalf("expected updates in return record")
	}

	targetRef := namespaceDestRef(t, fixture.returnRecord.Updates[0].Ref, fixture.exchange.ExchangeID, namespace)
	existingOID := strings.TrimSpace(fixture.sourceRepo.Git("rev-parse", "HEAD"))
	fixture.sourceRepo.Git("update-ref", targetRef, existingOID)

	plan, err := RunDryRun(Options{
		CWD:               fixture.sourceRepo.Dir,
		ReturnPackagePath: fixture.returnPackagePath,
		Namespace:         namespace,
	})
	if err != nil {
		t.Fatalf("RunDryRun returned error: %v", err)
	}

	entry, ok := findEntry(plan.Entries, targetRef)
	if !ok {
		t.Fatalf("expected namespace destination entry %q", targetRef)
	}
	if entry.Action != ActionConflict {
		t.Fatalf("expected conflict action, got %s", entry.Action)
	}
	if entry.ConflictCode != errs.CodeRefExists {
		t.Fatalf("expected REF_EXISTS conflict, got %s", entry.ConflictCode)
	}
}

type returnFixture struct {
	sourceRepo        *gitrepo.Repo
	exportRoot        string
	exchange          exchange.Exchange
	returnPackagePath string
	returnRecord      exchange.Return
}

func setupReturnFixture(t *testing.T, mutate func(t *testing.T, exportRoot string, ex exchange.Exchange)) returnFixture {
	t.Helper()

	source := gitrepo.New(t)
	source.WriteFile("README.md", "source\n")
	source.CommitAll("initial")
	source.Git("branch", "feature/context")
	source.Git("tag", "v1.0.0")

	helperPath := writeHelperBinary(t)
	prepResult, err := prepare.Run(prepare.Options{
		CWD:         source.Dir,
		ToolVersion: "0.1.0-test",
		HelperPath:  helperPath,
		Now:         func() time.Time { return time.Date(2026, time.March, 20, 11, 30, 15, 0, time.UTC) },
		Random:      bytes.NewReader([]byte{0x0a, 0x0b, 0x0c}),
	})
	if err != nil {
		t.Fatalf("prepare.Run failed: %v", err)
	}

	exportRoot := unzipTaskPackage(t, prepResult.TaskPackagePath)
	ex, err := exchange.ReadExchange(filepath.Join(exportRoot, ".git", "procoder", "exchange.json"))
	if err != nil {
		t.Fatalf("ReadExchange(export) failed: %v", err)
	}

	if mutate != nil {
		mutate(t, exportRoot, ex)
	}

	retResult, err := returnpkg.Run(returnpkg.Options{
		CWD:         exportRoot,
		ToolVersion: "0.1.0-test",
		Now:         func() time.Time { return time.Date(2026, time.March, 20, 12, 5, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("returnpkg.Run failed: %v", err)
	}

	retRecord := readReturnRecordFromZip(t, retResult.ReturnPackagePath)
	return returnFixture{
		sourceRepo:        source,
		exportRoot:        exportRoot,
		exchange:          ex,
		returnPackagePath: retResult.ReturnPackagePath,
		returnRecord:      retRecord,
	}
}

func mutateTaskRootCommit(t *testing.T, exportRoot string, ex exchange.Exchange) {
	t.Helper()

	appendFile(t, filepath.Join(exportRoot, "README.md"), "remote task change\n")
	runGit(t, exportRoot, "add", "README.md")
	runGit(t, exportRoot, "commit", "-m", "task update")

	currentRef := strings.TrimSpace(runGit(t, exportRoot, "symbolic-ref", "--quiet", "HEAD"))
	if currentRef != ex.Task.RootRef {
		t.Fatalf("expected export repo on task root ref %q, got %q", ex.Task.RootRef, currentRef)
	}
}

func mutateSiblingTaskBranchCommit(t *testing.T, exportRoot string, ex exchange.Exchange) {
	t.Helper()

	taskShort := strings.TrimPrefix(ex.Task.RootRef, "refs/heads/")
	siblingRef := ex.Task.RefPrefix + "/experiment"
	siblingShort := strings.TrimPrefix(siblingRef, "refs/heads/")

	runGit(t, exportRoot, "branch", siblingShort, taskShort)
	runGit(t, exportRoot, "checkout", siblingShort)
	appendFile(t, filepath.Join(exportRoot, "README.md"), "sibling work\n")
	runGit(t, exportRoot, "add", "README.md")
	runGit(t, exportRoot, "commit", "-m", "experiment")
	runGit(t, exportRoot, "checkout", taskShort)
}

func namespaceDestRef(t *testing.T, sourceRef, exchangeID, namespace string) string {
	t.Helper()

	prefix := exchange.TaskRefPrefix(exchangeID)
	if !strings.HasPrefix(sourceRef, prefix+"/") {
		t.Fatalf("source ref %q is outside task family prefix %q", sourceRef, prefix)
	}
	suffix := strings.TrimPrefix(sourceRef, prefix)
	return "refs/heads/" + namespace + "/" + exchangeID + suffix
}

func findEntry(entries []PlanEntry, destinationRef string) (PlanEntry, bool) {
	for _, entry := range entries {
		if entry.DestinationRef == destinationRef || entry.SourceRef == destinationRef {
			return entry, true
		}
	}
	return PlanEntry{}, false
}

func readReturnRecordFromZip(t *testing.T, zipPath string) exchange.Return {
	t.Helper()

	extracted := unzipFile(t, zipPath)
	record, err := exchange.ReadReturn(filepath.Join(extracted, returnJSONName))
	if err != nil {
		t.Fatalf("ReadReturn failed: %v", err)
	}
	return record
}

func rewriteReturnPackage(t *testing.T, src string, mutate func(files map[string][]byte)) string {
	t.Helper()

	files := readZipFiles(t, src)
	mutate(files)
	dst := filepath.Join(t.TempDir(), filepath.Base(src))
	writeZip(t, dst, files)
	return dst
}

func readZipFiles(t *testing.T, zipPath string) map[string][]byte {
	t.Helper()

	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("zip.OpenReader failed: %v", err)
	}
	defer reader.Close()

	files := make(map[string][]byte)
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		in, err := file.Open()
		if err != nil {
			t.Fatalf("open zip entry failed: %v", err)
		}
		data, err := io.ReadAll(in)
		_ = in.Close()
		if err != nil {
			t.Fatalf("read zip entry failed: %v", err)
		}
		files[file.Name] = data
	}
	return files
}

func writeZip(t *testing.T, zipPath string, files map[string][]byte) {
	t.Helper()

	out, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip failed: %v", err)
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
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %q failed: %v", name, err)
		}
		if _, err := w.Write(files[name]); err != nil {
			t.Fatalf("write zip entry %q failed: %v", name, err)
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
