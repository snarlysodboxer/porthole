package tools

// Views declaratively enrich resource_conditions with per-kind facts. The
// extractor is scalar-only by construction: a fact whose leaves include a
// map or object yields nothing, so a view cannot smuggle whole object
// bodies and the closed-response-types rule holds structurally. Which
// fields are safe to expose is the operator's call - porthole cannot know
// which of a CRD's fields hold secret material.

import (
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/snarlysodboxer/porthole/internal/config"
)

// extractFacts pulls a view's facts out of an object. Facts whose paths are
// missing, or whose leaves are not all scalars, are omitted.
func extractFacts(v *config.View, obj *unstructured.Unstructured) []Fact {
	var out []Fact
	for _, f := range v.Facts {
		// Paths were validated at config load; a parse error here means a
		// programmatically-built config, and the fact is just skipped.
		path, err := config.ParseFactPath(f.Path)
		if err != nil {
			continue
		}
		elements := leavesAt(obj.Object, path.Segments)
		if len(elements) == 0 {
			continue
		}
		var value string
		var ok bool
		if len(path.Projection) > 0 {
			value, ok = renderProjection(elements, path.Projection)
		} else {
			value, ok = renderLeaves(elements)
		}
		if !ok || value == "" {
			continue
		}
		out = append(out, Fact{Name: f.Name, Value: value})
	}

	return out
}

// renderLeaves renders plain leaves, refusing the lot if any is not a
// scalar.
func renderLeaves(leaves []any) (string, bool) {
	parts := make([]string, 0, len(leaves))
	for _, leaf := range leaves {
		s, ok := renderScalar(leaf)
		if !ok {
			return "", false
		}
		parts = append(parts, s)
	}

	return strings.Join(parts, ", "), true
}

// renderProjection renders each element as "{label: value, ...}", keeping
// an element's fields associated with each other. Entries missing on an
// element are skipped; an entry landing on a non-scalar refuses the whole
// fact.
func renderProjection(elements []any, projection []config.ProjectionEntry) (string, bool) {
	var rendered []string
	for _, element := range elements {
		var pairs []string
		for _, entry := range projection {
			leaves := leavesAt(element, entry.Segments)
			if len(leaves) == 0 {
				continue
			}
			value, ok := renderLeaves(leaves)
			if !ok {
				return "", false
			}
			pairs = append(pairs, entry.Label+": "+value)
		}
		if len(pairs) == 0 {
			continue
		}
		rendered = append(rendered, "{"+strings.Join(pairs, ", ")+"}")
	}

	return strings.Join(rendered, ", "), true
}

// leavesAt walks the parsed path, fanning out over [*] list operations and
// selecting elements for [N]. Missing fields and shape mismatches
// contribute nothing.
func leavesAt(v any, segments []config.PathSegment) []any {
	if len(segments) == 0 {
		return []any{v}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	current, ok := m[segments[0].Name]
	if !ok {
		return nil
	}
	values := []any{current}
	for _, index := range segments[0].Indexes {
		var next []any
		for _, item := range values {
			list, ok := item.([]any)
			if !ok {
				continue
			}
			if index == config.WildcardIndex {
				next = append(next, list...)
			} else if index < len(list) {
				next = append(next, list[index])
			}
		}
		values = next
	}
	var out []any
	for _, item := range values {
		out = append(out, leavesAt(item, segments[1:])...)
	}

	return out
}

// renderScalar formats scalars and joins lists of scalars; anything else -
// maps, objects, lists containing them - is refused.
func renderScalar(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64), true
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			s, ok := renderScalar(item)
			if !ok {
				return "", false
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, ", "), true
	default:
		return "", false
	}
}
