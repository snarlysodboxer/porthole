package tools

import (
	"regexp"
	"strings"
	"testing"
)

func TestShapeLogsPassthrough(t *testing.T) {
	out := shapeLogs([]byte("one\ntwo\nthree\n"), nil, 1024)
	if len(out.Lines) != 3 || out.Truncated {
		t.Fatalf("out = %+v", out)
	}
}

func TestShapeLogsGrep(t *testing.T) {
	data := []byte("info: ok\nerror: boom\ninfo: fine\nERROR: bang\n")
	out := shapeLogs(data, regexp.MustCompile(`(?i)error`), 1024)
	if out.MatchedLines != 2 || len(out.Lines) != 2 {
		t.Fatalf("out = %+v", out)
	}
	if out.Lines[0] != "error: boom" || out.Lines[1] != "ERROR: bang" {
		t.Errorf("lines = %v", out.Lines)
	}
}

func TestShapeLogsTruncationKeepsRecent(t *testing.T) {
	var b strings.Builder
	for range 100 {
		b.WriteString("0123456789\n")
	}
	out := shapeLogs([]byte(b.String()), nil, 55) // room for 5 lines of 11 bytes
	if !out.Truncated {
		t.Fatal("expected truncation")
	}
	if len(out.Lines) != 5 {
		t.Errorf("kept %d lines, want 5", len(out.Lines))
	}
	if !strings.Contains(out.Note, "truncated") {
		t.Errorf("note = %q, want explicit truncation marker", out.Note)
	}
}

func TestShapeLogsEmpty(t *testing.T) {
	out := shapeLogs(nil, nil, 1024)
	if out.Lines == nil || len(out.Lines) != 0 {
		t.Fatalf("out = %+v, want empty non-nil lines", out)
	}
}
