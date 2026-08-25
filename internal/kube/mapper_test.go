package kube

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	discoveryfake "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
)

type cachedFakeDiscovery struct {
	*discoveryfake.FakeDiscovery
}

func (c *cachedFakeDiscovery) Fresh() bool { return true }
func (c *cachedFakeDiscovery) Invalidate() {}

func newTestMapper() *Mapper {
	return NewMapper(&cachedFakeDiscovery{FakeDiscovery: &discoveryfake.FakeDiscovery{
		Fake: &clienttesting.Fake{Resources: []*metav1.APIResourceList{
			{
				GroupVersion: "apps/v1",
				APIResources: []metav1.APIResource{{Name: "deployments", Kind: "Deployment", Namespaced: true}},
			},
			{
				GroupVersion: "gateway.networking.k8s.io/v1",
				APIResources: []metav1.APIResource{{Name: "gateways", Kind: "Gateway", Namespaced: true}},
			},
			{
				GroupVersion: "networking.istio.io/v1",
				APIResources: []metav1.APIResource{{Name: "gateways", Kind: "Gateway", Namespaced: true}},
			},
			{
				GroupVersion: "cert-manager.io/v1",
				APIResources: []metav1.APIResource{
					{Name: "certificates", Kind: "Certificate", Namespaced: true},
					{Name: "certificates/status", Kind: "Certificate", Namespaced: true},
				},
			},
		}},
	}})
}

func TestResolveCaseInsensitive(t *testing.T) {
	m := newTestMapper()
	res, err := m.Resolve("certificate", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.GVR.Group != "cert-manager.io" || res.GVR.Resource != "certificates" || !res.Namespaced {
		t.Errorf("res = %+v", res)
	}
	if res.Kind != "Certificate" {
		t.Errorf("kind = %q, want canonical casing", res.Kind)
	}
}

func TestResolveAmbiguousListsGroups(t *testing.T) {
	m := newTestMapper()
	_, err := m.Resolve("Gateway", "")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err = %v, want ambiguity error", err)
	}
	if !strings.Contains(err.Error(), "gateway.networking.k8s.io") || !strings.Contains(err.Error(), "networking.istio.io") {
		t.Errorf("err should list candidate groups: %v", err)
	}
}

func TestResolveWithGroupDisambiguates(t *testing.T) {
	m := newTestMapper()
	res, err := m.Resolve("Gateway", "networking.istio.io")
	if err != nil {
		t.Fatal(err)
	}
	if res.GVR.Group != "networking.istio.io" {
		t.Errorf("res = %+v", res)
	}
}

func TestResolveUnknownKind(t *testing.T) {
	m := newTestMapper()
	if _, err := m.Resolve("Nonexistent", ""); err == nil {
		t.Fatal("expected error for unknown kind")
	}
	if _, err := m.Resolve("Certificate", "wrong.group.io"); err == nil {
		t.Fatal("expected error for wrong group")
	}
}
