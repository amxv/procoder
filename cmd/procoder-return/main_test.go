package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintHelpTerminology(t *testing.T) {
	var out bytes.Buffer
	printHelp(&out)

	got := out.String()
	if !strings.Contains(got, "return package") {
		t.Fatalf("expected return package terminology, got: %q", got)
	}
	if !strings.Contains(got, "prepared task package") {
		t.Fatalf("expected prepared task package terminology, got: %q", got)
	}
	if strings.Contains(got, "remote handoff flow") {
		t.Fatalf("unexpected stale wording in help output, got: %q", got)
	}
}
