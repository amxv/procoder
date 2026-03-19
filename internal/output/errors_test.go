package output

import (
	"errors"
	"strings"
	"testing"

	"github.com/amxv/procoder/internal/errs"
)

func TestFormatErrorTyped(t *testing.T) {
	err := errs.New(
		errs.CodeBranchMoved,
		"cannot update refs/heads/procoder/20260320-abc123",
		errs.WithDetails(
			"Expected old OID: abc123",
			"Current local OID: def456",
		),
		errs.WithHint("rerun with --namespace procoder-import"),
	)

	got := FormatError(err)
	wantLines := []string{
		"BRANCH_MOVED: cannot update refs/heads/procoder/20260320-abc123",
		"Expected old OID: abc123",
		"Current local OID: def456",
		"Hint: rerun with --namespace procoder-import",
	}
	for _, line := range wantLines {
		if !strings.Contains(got, line) {
			t.Fatalf("missing line %q in output:\n%s", line, got)
		}
	}
}

func TestFormatErrorUntyped(t *testing.T) {
	got := FormatError(errors.New("boom"))
	if got != "INTERNAL: boom\n" {
		t.Fatalf("unexpected fallback format: %q", got)
	}
}
