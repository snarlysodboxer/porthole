package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EventsInput filters events.
type EventsInput struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"namespace to query; omit to scan every allowed namespace"`
	Kind      string `json:"kind,omitempty" jsonschema:"filter to events about this kind (e.g. Pod, Deployment)"`
	Name      string `json:"name,omitempty" jsonschema:"filter to events about the object with this name (combine with kind)"`
	Since     string `json:"since,omitempty" jsonschema:"only events last seen within this window, as a Go duration like 30m or 2h (cluster retains ~1h by default)"`
}

// Events implements the events tool.
func (t *Toolset) Events(ctx context.Context, req *mcp.CallToolRequest, in EventsInput) (*mcp.CallToolResult, EventsOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	var since time.Duration
	if in.Since != "" {
		var err error
		if since, err = time.ParseDuration(in.Since); err != nil {
			return nil, EventsOutput{}, fmt.Errorf("invalid since %q: use a Go duration like 30m, 2h", in.Since)
		}
	}
	namespaces, err := t.namespacesFor(in.Namespace)
	if err != nil {
		return nil, EventsOutput{}, err
	}

	out := EventsOutput{Events: []EventInfo{}}
	var all []shapedEvent
	for _, ns := range namespaces {
		events, err := t.listEvents(ctx, ns, in.Kind, in.Name, since)
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("events in %q: %v", ns, err))
			continue
		}
		all = append(all, events...)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].lastSeen.Before(all[j].lastSeen) })
	if len(all) > t.cfg.MaxEvents {
		out.TruncatedCount = len(all) - t.cfg.MaxEvents
		all = all[len(all)-t.cfg.MaxEvents:] // keep the most recent
		out.Note = fmt.Sprintf("truncated: %d older events not shown", out.TruncatedCount)
	}
	for _, e := range all {
		out.Events = append(out.Events, e.info)
	}

	return nil, out, nil
}

type shapedEvent struct {
	info     EventInfo
	lastSeen time.Time
}

// listEvents lists events in one namespace, optionally filtered to a single
// object. It filters server-side via field selectors and re-filters
// client-side (belt and braces; also keeps fake-client tests honest).
func (t *Toolset) listEvents(ctx context.Context, ns, kind, name string, since time.Duration) ([]shapedEvent, error) {
	var selectors []string
	if kind != "" {
		selectors = append(selectors, "regarding.kind="+kind)
	}
	if name != "" {
		selectors = append(selectors, "regarding.name="+name)
	}
	list, err := t.clients.Typed.EventsV1().Events(ns).List(ctx, metav1.ListOptions{
		FieldSelector: strings.Join(selectors, ","),
	})
	if err != nil {
		return nil, err
	}

	var cutoff time.Time
	if since > 0 {
		cutoff = t.now().Add(-since)
	}
	var out []shapedEvent
	for i := range list.Items {
		e := &list.Items[i]
		if kind != "" && !strings.EqualFold(e.Regarding.Kind, kind) {
			continue
		}
		if name != "" && e.Regarding.Name != name {
			continue
		}
		shaped := shapeEvent(e)
		if !cutoff.IsZero() && shaped.lastSeen.Before(cutoff) {
			continue
		}
		out = append(out, shaped)
	}

	return out, nil
}

// fetchEvents returns shaped events for one object, capped at the server
// ceiling, for inlining into other tools' responses.
func (t *Toolset) fetchEvents(ctx context.Context, ns, kind, name string, since time.Duration) ([]EventInfo, int, error) {
	events, err := t.listEvents(ctx, ns, kind, name, since)
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(events, func(i, j int) bool { return events[i].lastSeen.Before(events[j].lastSeen) })
	truncated := 0
	if len(events) > t.cfg.MaxEvents {
		truncated = len(events) - t.cfg.MaxEvents
		events = events[len(events)-t.cfg.MaxEvents:]
	}
	infos := make([]EventInfo, 0, len(events))
	for _, e := range events {
		infos = append(infos, e.info)
	}

	return infos, truncated, nil
}

// shapeEvent normalizes an events.k8s.io/v1 Event, which spreads times and
// counts across new (eventTime, series) and deprecated fields depending on
// which component wrote it.
func shapeEvent(e *eventsv1.Event) shapedEvent {
	count := e.DeprecatedCount
	if e.Series != nil {
		count = e.Series.Count
	}
	if count == 0 {
		count = 1
	}

	firstSeen := e.DeprecatedFirstTimestamp.Time
	if firstSeen.IsZero() {
		firstSeen = e.EventTime.Time
	}
	lastSeen := e.DeprecatedLastTimestamp.Time
	if e.Series != nil && !e.Series.LastObservedTime.IsZero() {
		lastSeen = e.Series.LastObservedTime.Time
	}
	if lastSeen.IsZero() {
		lastSeen = e.EventTime.Time
	}
	if lastSeen.IsZero() {
		lastSeen = e.CreationTimestamp.Time
	}

	source := e.ReportingController
	if source == "" {
		source = e.DeprecatedSource.Component
	}

	fmtT := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}

	return shapedEvent{
		lastSeen: lastSeen,
		info: EventInfo{
			Type:      e.Type,
			Reason:    e.Reason,
			Message:   e.Note,
			Count:     count,
			FirstSeen: fmtT(firstSeen),
			LastSeen:  fmtT(lastSeen),
			Object:    e.Regarding.Kind + "/" + e.Regarding.Name,
			Source:    source,
		},
	}
}
