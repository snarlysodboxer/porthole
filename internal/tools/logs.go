package tools

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
)

// PodLogsInput selects a container's logs.
type PodLogsInput struct {
	Namespace string `json:"namespace" jsonschema:"the pod's namespace"`
	Pod       string `json:"pod" jsonschema:"the pod's name"`
	Container string `json:"container,omitempty" jsonschema:"container name; defaults to the pod's only or first container"`
	TailLines int64  `json:"tail_lines,omitempty" jsonschema:"return only the last N lines"`
	Previous  bool   `json:"previous,omitempty" jsonschema:"return the previous (crashed) container instance's logs — key for crash loops"`
	Grep      string `json:"grep,omitempty" jsonschema:"RE2 regex; only matching lines are returned (applied server-side)"`
	Since     string `json:"since,omitempty" jsonschema:"only logs newer than this window, as a Go duration like 15m or 1h"`
	MaxBytes  int64  `json:"max_bytes,omitempty" jsonschema:"cap on returned bytes; clamped to the server's ceiling"`
}

// PodLogs implements the flag-gated pod_logs tool.
func (t *Toolset) PodLogs(ctx context.Context, req *mcp.CallToolRequest, in PodLogsInput) (*mcp.CallToolResult, PodLogsOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	if err := t.checkNamespace(in.Namespace); err != nil {
		return nil, PodLogsOutput{}, err
	}
	var grep *regexp.Regexp
	if in.Grep != "" {
		var err error
		if grep, err = regexp.Compile(in.Grep); err != nil {
			return nil, PodLogsOutput{}, fmt.Errorf("invalid grep regex: %w", err)
		}
	}

	maxBytes := t.cfg.MaxLogBytes
	if in.MaxBytes > 0 && in.MaxBytes < maxBytes {
		maxBytes = in.MaxBytes
	}

	opts := &corev1.PodLogOptions{
		Container: in.Container,
		Previous:  in.Previous,
	}
	if in.TailLines > 0 {
		opts.TailLines = &in.TailLines
	}
	if in.Since != "" {
		d, err := time.ParseDuration(in.Since)
		if err != nil {
			return nil, PodLogsOutput{}, fmt.Errorf("invalid since %q: use a Go duration like 15m, 1h", in.Since)
		}
		secs := int64(d.Seconds())
		opts.SinceSeconds = &secs
	}
	// When grep-filtering, read more than the response cap so matches
	// deep in the window survive; the kubelet caps the read either way.
	fetchLimit := maxBytes
	if grep != nil {
		fetchLimit = t.cfg.MaxLogBytes * 4
	}
	opts.LimitBytes = &fetchLimit

	stream, err := t.clients.Typed.CoreV1().Pods(in.Namespace).GetLogs(in.Pod, opts).Stream(ctx)
	if err != nil {
		return nil, PodLogsOutput{}, err
	}
	defer func() { _ = stream.Close() }()
	data, err := io.ReadAll(io.LimitReader(stream, fetchLimit+1))
	if err != nil {
		return nil, PodLogsOutput{}, fmt.Errorf("reading log stream: %w", err)
	}
	sourceTruncated := int64(len(data)) > fetchLimit
	if sourceTruncated {
		data = data[:fetchLimit]
	}

	out := shapeLogs(data, grep, maxBytes)
	out.Truncated = out.Truncated || sourceTruncated
	if out.Truncated && out.Note == "" {
		out.Note = "truncated: log window exceeded the size cap"
	}

	return nil, out, nil
}

// shapeLogs applies the grep filter and the response byte cap. Split out
// for direct testing.
func shapeLogs(data []byte, grep *regexp.Regexp, maxBytes int64) PodLogsOutput {
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}

	out := PodLogsOutput{Lines: []string{}}
	matched := 0
	var kept []string
	for _, line := range lines {
		if grep != nil && !grep.MatchString(line) {
			continue
		}
		matched++
		kept = append(kept, line)
	}
	if grep != nil {
		out.MatchedLines = matched
	}

	// Keep the most recent lines that fit under the byte cap.
	var size int64
	start := len(kept)
	for i := len(kept) - 1; i >= 0; i-- {
		size += int64(len(kept[i])) + 1
		if size > maxBytes {
			break
		}
		start = i
	}
	if start > 0 {
		out.Truncated = true
		out.Note = fmt.Sprintf("truncated: %d earlier lines dropped to fit the size cap (%d lines matched)", start, matched)
	}
	out.Lines = append(out.Lines, kept[start:]...)

	return out
}
