package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WebhookConfigsInput has no parameters.
type WebhookConfigsInput struct{}

// WebhookConfigs implements the webhook_configs tool: cluster-scoped,
// spec-only. The classic failure it diagnoses is failurePolicy=Fail
// pointing at a dead service. caBundle is omitted by construction.
func (t *Toolset) WebhookConfigs(ctx context.Context, req *mcp.CallToolRequest, in WebhookConfigsInput) (*mcp.CallToolResult, WebhookConfigsOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	out := WebhookConfigsOutput{Mutating: []WebhookConfigInfo{}, Validating: []WebhookConfigInfo{}}

	mutating, err := t.clients.Typed.AdmissionregistrationV1().MutatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("mutatingwebhookconfigurations: %v", err))
	} else {
		for i := range mutating.Items {
			cfg := &mutating.Items[i]
			info := WebhookConfigInfo{Name: cfg.Name}
			for _, wh := range cfg.Webhooks {
				info.Webhooks = append(info.Webhooks, shapeWebhook(
					wh.Name, wh.ClientConfig, (*string)(wh.FailurePolicy), wh.TimeoutSeconds,
					(*string)(wh.SideEffects), wh.Rules, wh.NamespaceSelector, wh.ObjectSelector,
				))
			}
			out.Mutating = append(out.Mutating, info)
		}
	}

	validating, err := t.clients.Typed.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("validatingwebhookconfigurations: %v", err))
	} else {
		for i := range validating.Items {
			cfg := &validating.Items[i]
			info := WebhookConfigInfo{Name: cfg.Name}
			for _, wh := range cfg.Webhooks {
				info.Webhooks = append(info.Webhooks, shapeWebhook(
					wh.Name, wh.ClientConfig, (*string)(wh.FailurePolicy), wh.TimeoutSeconds,
					(*string)(wh.SideEffects), wh.Rules, wh.NamespaceSelector, wh.ObjectSelector,
				))
			}
			out.Validating = append(out.Validating, info)
		}
	}

	return nil, out, nil
}

func shapeWebhook(name string, cc admissionv1.WebhookClientConfig, failurePolicy *string, timeout *int32,
	sideEffects *string, rules []admissionv1.RuleWithOperations, nsSel, objSel *metav1.LabelSelector) WebhookInfo {
	info := WebhookInfo{
		Name:              name,
		Service:           shapeWebhookService(cc),
		Rules:             shapeWebhookRules(rules),
		NamespaceSelector: formatSelector(nsSel),
		ObjectSelector:    formatSelector(objSel),
	}
	if failurePolicy != nil {
		info.FailurePolicy = *failurePolicy
	}
	if timeout != nil {
		info.TimeoutSeconds = *timeout
	}
	if sideEffects != nil {
		info.SideEffects = *sideEffects
	}

	return info
}

// shapeWebhookService renders the backing service reference - names only.
// URL-based webhooks report only that a URL is configured: webhook URLs are
// operator-supplied free text and can embed credentials or internal hosts.
func shapeWebhookService(cc admissionv1.WebhookClientConfig) string {
	if cc.Service != nil {
		s := cc.Service.Namespace + "/" + cc.Service.Name
		if cc.Service.Port != nil {
			s += fmt.Sprintf(":%d", *cc.Service.Port)
		}
		if cc.Service.Path != nil {
			s += *cc.Service.Path
		}
		return s
	}
	if cc.URL != nil {
		return "url (value omitted)"
	}

	return ""
}

func shapeWebhookRules(rules []admissionv1.RuleWithOperations) []string {
	var out []string
	for _, r := range rules {
		ops := make([]string, 0, len(r.Operations))
		for _, o := range r.Operations {
			ops = append(ops, string(o))
		}
		groups := make([]string, 0, len(r.APIGroups))
		for _, g := range r.APIGroups {
			if g == "" {
				g = "core"
			}
			groups = append(groups, g)
		}
		out = append(out, fmt.Sprintf("%s %s/%s",
			strings.Join(ops, ","), strings.Join(groups, ","), strings.Join(r.Resources, ",")))
	}

	return out
}
