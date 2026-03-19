package exchange

import (
	"bytes"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"
)

func TestGenerateID(t *testing.T) {
	now := time.Date(2026, time.March, 20, 11, 30, 15, 0, time.UTC)
	id, err := GenerateID(now, bytes.NewReader([]byte{0xAA, 0x01, 0xBC}))
	if err != nil {
		t.Fatalf("GenerateID returned error: %v", err)
	}

	const expected = "20260320-113015-aa01bc"
	if id != expected {
		t.Fatalf("unexpected id: got %q want %q", id, expected)
	}

	matched, err := regexp.MatchString(`^\d{8}-\d{6}-[0-9a-f]{6}$`, id)
	if err != nil {
		t.Fatalf("regexp compile failed: %v", err)
	}
	if !matched {
		t.Fatalf("id does not match expected format: %q", id)
	}
}

func TestGenerateIDInsufficientRandomBytes(t *testing.T) {
	_, err := GenerateID(time.Now(), bytes.NewReader([]byte{0xAB}))
	if err == nil {
		t.Fatal("expected error for insufficient random bytes")
	}
}

func TestExchangeAndReturnJSONRoundTrip(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 20, 10, 30, 0, 0, time.UTC)
	ex := Exchange{
		Protocol:    ExchangeProtocolV1,
		ExchangeID:  "20260320-103000-a1b2c3",
		CreatedAt:   now,
		ToolVersion: "0.1.0",
		Source: ExchangeSource{
			HeadRef: "refs/heads/main",
			HeadOID: "abc123",
		},
		Task: ExchangeTask{
			RootRef:   "refs/heads/procoder/20260320-103000-a1b2c3",
			RefPrefix: "refs/heads/procoder/20260320-103000-a1b2c3",
			BaseOID:   "abc123",
		},
		Context: ExchangeContext{
			Heads: map[string]string{
				"refs/heads/main": "abc123",
			},
			Tags: map[string]string{
				"refs/tags/v1.0.0": "def456",
			},
		},
	}

	ret := Return{
		Protocol:    ReturnProtocolV1,
		ExchangeID:  ex.ExchangeID,
		CreatedAt:   now.Add(30 * time.Minute),
		ToolVersion: "0.1.0",
		BundleFile:  "procoder-return.bundle",
		Task: ReturnTask{
			RootRef: ex.Task.RootRef,
			BaseOID: ex.Task.BaseOID,
		},
		Updates: []RefUpdate{
			{
				Ref:    ex.Task.RootRef,
				OldOID: "abc123",
				NewOID: "def456",
			},
		},
	}

	exPath := filepath.Join(t.TempDir(), "exchange.json")
	if err := WriteExchange(exPath, ex); err != nil {
		t.Fatalf("WriteExchange failed: %v", err)
	}
	gotEx, err := ReadExchange(exPath)
	if err != nil {
		t.Fatalf("ReadExchange failed: %v", err)
	}
	if !reflect.DeepEqual(ex, gotEx) {
		t.Fatalf("exchange mismatch after round trip:\n got: %#v\nwant: %#v", gotEx, ex)
	}

	retPath := filepath.Join(t.TempDir(), "procoder-return.json")
	if err := WriteReturn(retPath, ret); err != nil {
		t.Fatalf("WriteReturn failed: %v", err)
	}
	gotRet, err := ReadReturn(retPath)
	if err != nil {
		t.Fatalf("ReadReturn failed: %v", err)
	}
	if !reflect.DeepEqual(ret, gotRet) {
		t.Fatalf("return mismatch after round trip:\n got: %#v\nwant: %#v", gotRet, ret)
	}
}

func TestIsTaskRef(t *testing.T) {
	exchangeID := "20260320-113015-a1b2c3"
	root := TaskRootRef(exchangeID)

	if !IsTaskRef(exchangeID, root) {
		t.Fatalf("expected root ref to be allowed: %q", root)
	}
	child := root + "/experiment"
	if !IsTaskRef(exchangeID, child) {
		t.Fatalf("expected child ref to be allowed: %q", child)
	}
	if IsTaskRef(exchangeID, "refs/heads/main") {
		t.Fatal("did not expect refs/heads/main to be allowed")
	}
}

func TestTaskRefHelpersFailClosedForInvalidExchangeID(t *testing.T) {
	t.Parallel()

	refInNamespace := "refs/heads/procoder/20260320-113015-a1b2c3"
	testCases := []struct {
		name string
		id   string
	}{
		{name: "empty", id: ""},
		{name: "spaces", id: "   "},
		{name: "date-only", id: "20260320"},
		{name: "missing-random", id: "20260320-113015"},
		{name: "contains-slash", id: "20260320-113015-a1b2c3/child"},
		{name: "path-traversal", id: "../20260320-113015-a1b2c3"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := TaskRootRef(tc.id)
			if root != "" {
				t.Fatalf("expected empty root for invalid exchange ID %q, got %q", tc.id, root)
			}
			if prefix := TaskRefPrefix(tc.id); prefix != "" {
				t.Fatalf("expected empty prefix for invalid exchange ID %q, got %q", tc.id, prefix)
			}
			if IsTaskRef(tc.id, refInNamespace) {
				t.Fatalf("expected IsTaskRef false for invalid exchange ID %q", tc.id)
			}
		})
	}
}
