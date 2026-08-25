package kube

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// Mapper resolves a kind name (optionally qualified by an API group) to a
// GroupVersionResource using cached API discovery.
type Mapper struct {
	mu    sync.Mutex
	disco discovery.CachedDiscoveryInterface
}

// NewMapper wraps a cached discovery client.
func NewMapper(disco discovery.CachedDiscoveryInterface) *Mapper {
	return &Mapper{disco: disco}
}

// Resolution is the result of resolving a kind.
type Resolution struct {
	GVR        schema.GroupVersionResource
	Kind       string
	Namespaced bool
}

// Resolve matches kind case-insensitively against the server's preferred
// resources. If the kind exists in multiple API groups and group is empty,
// it returns an error listing the candidate groups.
func (m *Mapper) Resolve(kind, group string) (*Resolution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	candidates, err := m.candidates(kind, group)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		// The cached discovery data may predate a newly installed CRD.
		m.disco.Invalidate()
		if candidates, err = m.candidates(kind, group); err != nil {
			return nil, err
		}
	}
	switch len(candidates) {
	case 0:
		if group != "" {
			return nil, fmt.Errorf("no resource found for kind %q in group %q", kind, group)
		}
		return nil, fmt.Errorf("no resource found for kind %q", kind)
	case 1:
		return candidates[0], nil
	default:
		groups := make([]string, 0, len(candidates))
		for _, c := range candidates {
			g := c.GVR.Group
			if g == "" {
				g = "(core)"
			}
			groups = append(groups, g)
		}
		sort.Strings(groups)
		return nil, fmt.Errorf("kind %q is ambiguous across API groups [%s]: pass the group parameter to disambiguate",
			kind, strings.Join(groups, ", "))
	}
}

func (m *Mapper) candidates(kind, group string) ([]*Resolution, error) {
	// The package-level helper (rather than the interface method) applies
	// preferred-version selection uniformly, including over fakes in tests.
	lists, err := discovery.ServerPreferredResources(m.disco)
	// Partial discovery failures (e.g. one broken aggregated API) still
	// return usable lists; only fail when nothing came back.
	if err != nil && len(lists) == 0 {
		return nil, fmt.Errorf("discovering API resources: %w", err)
	}

	var out []*Resolution
	seen := map[string]bool{}
	for _, list := range lists {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		if group != "" && gv.Group != group {
			continue
		}
		for _, r := range list.APIResources {
			// Skip subresources like pods/log.
			if strings.Contains(r.Name, "/") {
				continue
			}
			if !strings.EqualFold(r.Kind, kind) {
				continue
			}
			if seen[gv.Group] {
				continue
			}
			seen[gv.Group] = true
			out = append(out, &Resolution{
				GVR:        gv.WithResource(r.Name),
				Kind:       r.Kind,
				Namespaced: r.Namespaced,
			})
		}
	}

	return out, nil
}
