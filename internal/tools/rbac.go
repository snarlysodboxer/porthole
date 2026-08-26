package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RBACSummaryInput selects a namespace.
type RBACSummaryInput struct {
	Namespace string `json:"namespace" jsonschema:"namespace whose RBAC objects to summarize"`
}

// RBACSummary implements the rbac_summary tool. RBAC objects are spec-only,
// so this is a shaped listing rather than a status read. ClusterRole
// resolution and ClusterRoleBinding listing need the optional cluster-scoped
// grant and degrade gracefully without it.
func (t *Toolset) RBACSummary(ctx context.Context, req *mcp.CallToolRequest, in RBACSummaryInput) (*mcp.CallToolResult, RBACSummaryOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	if err := t.checkNamespace(in.Namespace); err != nil {
		return nil, RBACSummaryOutput{}, err
	}
	out := RBACSummaryOutput{
		Namespace:       in.Namespace,
		ServiceAccounts: []ServiceAccountInfo{},
		Roles:           []RoleInfo{},
		RoleBindings:    []BindingInfo{},
	}

	sas, err := t.clients.Typed.CoreV1().ServiceAccounts(in.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("serviceaccounts: %v", err))
	} else {
		for i := range sas.Items {
			sa := &sas.Items[i]
			info := ServiceAccountInfo{
				Name:      sa.Name,
				Automount: sa.AutomountServiceAccountToken,
				CreatedAt: fmtTime(sa.CreationTimestamp),
			}
			for _, s := range sa.Secrets {
				info.Secrets = append(info.Secrets, s.Name)
			}
			for _, s := range sa.ImagePullSecrets {
				info.ImagePullSecrets = append(info.ImagePullSecrets, s.Name)
			}
			out.ServiceAccounts = append(out.ServiceAccounts, info)
		}
	}

	roles, err := t.clients.Typed.RbacV1().Roles(in.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("roles: %v", err))
	} else {
		for i := range roles.Items {
			out.Roles = append(out.Roles, RoleInfo{
				Name:  roles.Items[i].Name,
				Rules: shapeRules(roles.Items[i].Rules),
			})
		}
	}

	resolve := t.newClusterRoleResolver()
	rbs, err := t.clients.Typed.RbacV1().RoleBindings(in.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("rolebindings: %v", err))
	} else {
		for i := range rbs.Items {
			rb := &rbs.Items[i]
			info := BindingInfo{
				Name:     rb.Name,
				RoleRef:  rb.RoleRef.Kind + "/" + rb.RoleRef.Name,
				Subjects: subjectStrings(rb.Subjects),
			}
			if rb.RoleRef.Kind == "ClusterRole" {
				rules, err := resolve.rules(ctx, rb.RoleRef.Name)
				switch {
				case err == nil:
					info.RoleRefRules = rules
				case !resolve.denied:
					// A dangling or unreadable roleRef is itself a finding;
					// the no-grant case is reported once below instead.
					out.Errors = append(out.Errors, fmt.Sprintf("resolving ClusterRole %q for RoleBinding %q: %v", rb.RoleRef.Name, rb.Name, err))
				}
			}
			out.RoleBindings = append(out.RoleBindings, info)
		}
	}

	// ClusterRoleBindings are cluster-scoped: list only those whose
	// subjects reach into this namespace, and degrade without the grant.
	crbs, err := t.clients.Typed.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("clusterrolebindings not readable (namespaced RBAC info is still complete): %v", err))
	} else {
		for i := range crbs.Items {
			crb := &crbs.Items[i]
			if !crbTouchesNamespace(crb, in.Namespace) {
				continue
			}
			info := BindingInfo{
				Name:     crb.Name,
				RoleRef:  crb.RoleRef.Kind + "/" + crb.RoleRef.Name,
				Subjects: subjectStrings(crb.Subjects),
			}
			rules, err := resolve.rules(ctx, crb.RoleRef.Name)
			switch {
			case err == nil:
				info.RoleRefRules = rules
			case !resolve.denied:
				out.Errors = append(out.Errors, fmt.Sprintf("resolving ClusterRole %q for ClusterRoleBinding %q: %v", crb.RoleRef.Name, crb.Name, err))
			}
			out.ClusterRoleBindings = append(out.ClusterRoleBindings, info)
		}
	}
	if resolve.denied {
		out.Errors = append(out.Errors, "clusterroles not readable: roleRefs to ClusterRoles are reported unresolved")
	}

	return nil, out, nil
}

// ServiceAccountAccessInput identifies one ServiceAccount.
type ServiceAccountAccessInput struct {
	Namespace string `json:"namespace" jsonschema:"the ServiceAccount's namespace"`
	Name      string `json:"name" jsonschema:"the ServiceAccount's name"`
}

// ServiceAccountAccess implements the service_account_access tool: the
// effective rule list for one ServiceAccount, with per-binding provenance.
func (t *Toolset) ServiceAccountAccess(ctx context.Context, req *mcp.CallToolRequest, in ServiceAccountAccessInput) (*mcp.CallToolResult, ServiceAccountAccessOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	if err := t.checkNamespace(in.Namespace); err != nil {
		return nil, ServiceAccountAccessOutput{}, err
	}
	if _, err := t.clients.Typed.CoreV1().ServiceAccounts(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{}); err != nil {
		return nil, ServiceAccountAccessOutput{}, err
	}
	out := ServiceAccountAccessOutput{Namespace: in.Namespace, Name: in.Name, Access: []AccessRule{}}
	resolve := t.newClusterRoleResolver()

	appendAccess := func(bindingKind, bindingName string, roleRef rbacv1.RoleRef, via string) {
		source := fmt.Sprintf("%s/%s -> %s/%s", bindingKind, bindingName, roleRef.Kind, roleRef.Name)
		if via != "" {
			source += " (via " + via + ")"
		}
		rule := AccessRule{Source: source}
		var err error
		if roleRef.Kind == "ClusterRole" {
			rule.Rules, err = resolve.rules(ctx, roleRef.Name)
		} else {
			rule.Rules, err = t.roleRules(ctx, in.Namespace, roleRef.Name)
		}
		if err != nil {
			rule.Unresolved = fmt.Sprintf("could not read %s/%s: %v", roleRef.Kind, roleRef.Name, err)
		}
		out.Access = append(out.Access, rule)
	}

	rbs, err := t.clients.Typed.RbacV1().RoleBindings(in.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("rolebindings: %v", err))
	} else {
		for i := range rbs.Items {
			rb := &rbs.Items[i]
			if via, ok := subjectsMatchServiceAccount(rb.Subjects, in.Namespace, in.Name); ok {
				appendAccess("RoleBinding", rb.Name, rb.RoleRef, via)
			}
		}
	}

	crbs, err := t.clients.Typed.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("clusterrolebindings not readable (namespaced bindings are still complete): %v", err))
	} else {
		for i := range crbs.Items {
			crb := &crbs.Items[i]
			if via, ok := subjectsMatchServiceAccount(crb.Subjects, in.Namespace, in.Name); ok {
				appendAccess("ClusterRoleBinding", crb.Name, crb.RoleRef, via)
			}
		}
	}

	return nil, out, nil
}

// clusterRoleResolver caches ClusterRole rule lookups within one request
// and remembers a denied read, so a missing grant is reported once instead
// of retried per binding.
type clusterRoleResolver struct {
	t      *Toolset
	cache  map[string][]PolicyRuleInfo
	denied bool
}

func (t *Toolset) newClusterRoleResolver() *clusterRoleResolver {
	return &clusterRoleResolver{t: t, cache: map[string][]PolicyRuleInfo{}}
}

func (r *clusterRoleResolver) rules(ctx context.Context, name string) ([]PolicyRuleInfo, error) {
	if rules, ok := r.cache[name]; ok {
		return rules, nil
	}
	if r.denied {
		return nil, fmt.Errorf("clusterroles not readable")
	}
	cr, err := r.t.clients.Typed.RbacV1().ClusterRoles().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsForbidden(err) {
			r.denied = true
		}
		return nil, err
	}
	rules := shapeRules(cr.Rules)
	r.cache[name] = rules

	return rules, nil
}

func shapeRules(rules []rbacv1.PolicyRule) []PolicyRuleInfo {
	var out []PolicyRuleInfo
	for _, r := range rules {
		out = append(out, PolicyRuleInfo{
			APIGroups:       r.APIGroups,
			Resources:       r.Resources,
			ResourceNames:   r.ResourceNames,
			NonResourceURLs: r.NonResourceURLs,
			Verbs:           r.Verbs,
		})
	}

	return out
}

func subjectStrings(subjects []rbacv1.Subject) []string {
	var out []string
	for _, s := range subjects {
		if s.Kind == "ServiceAccount" {
			out = append(out, fmt.Sprintf("ServiceAccount:%s/%s", s.Namespace, s.Name))
			continue
		}
		out = append(out, s.Kind+":"+s.Name)
	}

	return out
}

// subjectsMatchServiceAccount reports whether a binding's subjects cover
// the given ServiceAccount, directly or via the well-known groups, and how.
func subjectsMatchServiceAccount(subjects []rbacv1.Subject, ns, name string) (via string, ok bool) {
	for _, s := range subjects {
		switch s.Kind {
		case "ServiceAccount":
			if s.Name == name && s.Namespace == ns {
				return "", true
			}
		case "Group":
			switch s.Name {
			case "system:serviceaccounts", "system:serviceaccounts:" + ns, "system:authenticated":
				return "Group:" + s.Name, true
			}
		}
	}

	return "", false
}

// crbTouchesNamespace reports whether a ClusterRoleBinding's subjects reach
// into the namespace: a ServiceAccount there, or a group covering it.
func crbTouchesNamespace(crb *rbacv1.ClusterRoleBinding, ns string) bool {
	for _, s := range crb.Subjects {
		if s.Kind == "ServiceAccount" && s.Namespace == ns {
			return true
		}
		if s.Kind == "Group" {
			switch s.Name {
			case "system:serviceaccounts", "system:serviceaccounts:" + ns, "system:authenticated":
				return true
			}
		}
	}

	return false
}

func (t *Toolset) roleRules(ctx context.Context, ns, name string) ([]PolicyRuleInfo, error) {
	role, err := t.clients.Typed.RbacV1().Roles(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return shapeRules(role.Rules), nil
}
