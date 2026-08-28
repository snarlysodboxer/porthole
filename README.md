# porthole

A curated, read-only Kubernetes MCP server for AI-assisted
troubleshooting - a sealed window you can look through but not reach
through.

porthole lets an AI agent (Claude Code, or any MCP client) diagnose workloads
on a cluster without ever seeing secret material or holding cluster credentials
of its own. Diagnose CrashLoopBackoff, failed rollouts, stuck PVCs, empty
Service endpoints, unhappy custom resources and more.

## Why not just read-only RBAC?

Kubernetes RBAC has no field-level semantics. Even a "read-only, no Secrets"
role leaks secret material through `env` values in Pod specs, `command`/`args`,
and the `kubectl.kubernetes.io/last-applied-configuration` annotation.

Generic read-only Kubernetes MCP servers pass raw objects through, so they
inherit all of that. porthole takes a different approach: for troubleshooting,
an agent mostly needs live state - `.status` fields and Events, and a list of
safe spec facts such as images, ports, resource requests, env variable names, etc.

## The safety property (and its limits)

**Claimed:** structural omission of spec and secret material. Every response is
a hand-shaped struct built only from safe fields. The struct definitions in
`internal/tools/types.go` are the security policy, and a leak test feeds every
tool adversarial objects with sentinel values planted in every denied field to
prove none survives serialization.

**Not claimed:** sanitization of free-text authored elsewhere. Event
messages and condition messages are controller-authored strings and can
occasionally quote configuration values. Container logs (behind a
feature flag, off by default) contain whatever your workloads print. If
your controllers or workloads write secrets into messages or logs, porthole
will faithfully relay them.

### Defense in depth

porthole is designed to be one layer of several:

1. **Response shaping** - this server (see above).
2. **Network** - in-cluster, restrict ingress with NetworkPolicy/mesh
   policy; reach it via `kubectl port-forward` so access requires the
   operator's own kubectl credentials and killing the port-forward is a
   hard off switch.
3. **RBAC** - run it under a dedicated ServiceAccount that can only
   `get`/`list` the served kinds in explicitly-bound namespaces.
4. **Audit** - all queries hit the API server as the dedicated
   ServiceAccount, cleanly distinguishable from human activity.

## Tools

| Tool | Answers | Notes |
|---|---|---|
| `list_namespaces` | "What can I look at?" | Allowlist + enabled features |
| `list_workloads` | "What's unhealthy here/anywhere?" | Deployments/StatefulSets/DaemonSets: replica counts, conditions. Omit `namespace` to fan out over the allowlist; `only_unhealthy=true` keeps broad scans cheap |
| `workload_status` | "Why isn't this rolling out?" | Conditions, observedGeneration vs generation, rollout revision, owned-pod summaries |
| `list_pods` | "What state are the pods in?" | Phase, ready x/y, restarts, node, per-container state reasons + exit codes |
| `pod_status` | Safe `kubectl describe pod` | Container states incl. last termination, conditions, QoS, images, env **names only**, volume names+types, resources, ports, events |
| `events` | "What happened?" | Filter by object and/or time window |
| `service_endpoints` | "Why is nothing answering?" | Selector, ports, ready/not-ready endpoint counts from EndpointSlices |
| `pvc_status` | "Why is the pod stuck Pending?" | PVC phase, size, storage class; bound PV detail; unbound PVs still referencing the namespace. PV detail needs the optional cluster-scoped grant and degrades gracefully without it |
| `job_status` | "Did the job/cron run?" | Job counts/conditions, CronJob schedule + last run times |
| `network_policies` | "Why can't X reach Y?" | Pod selectors, policy types, per-rule peer/port summaries |
| `rbac_summary` | "Why is this ServiceAccount getting 403s?" | ServiceAccounts, Roles with rules, RoleBindings; ClusterRole resolution and ClusterRoleBindings need the cluster grant and degrade without it |
| `service_account_access` | "What can this ServiceAccount actually do?" | Effective rules from every binding covering it - directly or via `system:` groups |
| `top_pods` | "What is using CPU/memory right now?" | Per-pod/per-container usage from metrics.k8s.io (needs metrics-server or equivalent), joined with each container's configured requests/limits |
| `node_status` | "Are the nodes healthy / where's headroom?" | Conditions, taints, allocatable vs capacity, usage percentages. Cluster grant |
| `storage_classes` | "Which storage classes exist, which is default?" | Provisioner, reclaim/binding mode, expansion. Parameters omitted (can reference secrets). Cluster grant |
| `webhook_configs` | "Why does every apply hang/fail?" | Failure policy, timeouts, backing service, rules, selectors; caBundle and webhook URLs omitted. Cluster grant |
| `list_api_resources` | "Is CRD X installed, at what version?" | Groups, versions, kinds from discovery - no extra RBAC at all |
| `secret_metadata` | "Does the Secret exist with the expected keys?" | Name, type, key names, age, owners - never values. Flag-gated, off by default; see the tradeoff below |
| `configmap_metadata` | "Does the ConfigMap exist with the expected keys?" | Same shape as `secret_metadata`. Flag-gated, off by default |
| `list_resources` | "What Applications exist, are they healthy?" | Enumerates any kind (incl. CRDs) with a per-item condition summary + view facts - the discovery step before `resource_conditions` |
| `resource_conditions` | Any kind's health, incl. CRDs | `.status.conditions` + events + view facts (below). Serves allowlisted cluster-scoped kinds via `--cluster-kinds` |
| `resource_status` | Deeper CRD inspection | Full `.status` + events; status fields with secret-looking names (`*password*`, `*token*`, `*secret*`, `*key*`, `*hash*`, `*credential*`) are redacted. Flag-gated, off by default |
| `pod_logs` | Crash-loop diagnosis | `previous=true` for crashed containers, server-side RE2 `grep`, explicit truncation markers. Flag-gated, off by default |

### Annotations

Annotations are omitted by default. `--allowed-annotations` (or
`allowedAnnotations` in the config file) is an exact-key allowlist copied
into responses on `pod_status`, `workload_status`, `resource_conditions`,
and `resource_status`. A built-in non-configurable denylist wins over it: any
key containing `last-applied` is never served, because last-applied-style
annotations embed whole object specs. Two annotations are read hardcoded
regardless of the allowlist: `deployment.kubernetes.io/revision` (rollout
revision) and `storageclass.kubernetes.io/is-default-class` (default
StorageClass marker) - both controller-written, single-value facts.

### Views: richer facts for the CRDs you care about

`resource_conditions` covers any kind generically, but common CRDs deserve
curated detail (a Certificate's `notAfter`, an Argo Application's sync
revision). Views are declarative config - no code, no plugins to build:

```yaml
views:
- group: cert-manager.io
  kind: Certificate
  facts:
  - name: notAfter
    path: .status.notAfter
```

When `resource_conditions` fetches a matching object, the response gains a
`details` list of `{name, value}` facts. Paths are dotted; they may fan out
over lists with `[*]`, pick one element with `[N]`, and end in a
`{a,b,...}` projection group that renders several fields per element while
keeping them associated - e.g.
`.spec.tolerations[*].{key,effect,operator}` →
`"{key: node.kubernetes.io/not-ready, effect: NoExecute, operator: Exists}, {...}"`.
A field missing on one element is skipped just for that element, and every
leaf must be a scalar: the extractor never copies maps or objects, so a
view cannot smuggle whole object bodies past the closed response types.
**The onus is on you to reference only fields that hold no
sensitive/secret material.**

A misconfigured view never takes the server down: views only enrich
`resource_conditions`, so a malformed view or an unparseable fact path is
dropped with a warning at startup and every other tool keeps serving.

Ready-made views for common CRDs (cert-manager, Argo CD, mariadb-operator,
grafana-operator) ship in the example kustomize config:
[`apps/porthole/base`](kustomize/apps/porthole/base) declares a first views
file, and [`overlays/prod/porthole`](kustomize/overlays/prod/porthole)
*appends* more - the pattern to copy. PRs adding views are welcome.

Cluster-scoped kinds (e.g. cert-manager's ClusterIssuer) are rejected by
the generic tools unless listed in `--cluster-kinds`, since reading them
needs a cluster-scoped grant.

### The Secret/ConfigMap metadata tradeoff

`secret_metadata` answers the missing-key crash loop ("the pod wants key
`password` - does the Secret have it?") by reporting key names, never
values: values have no field in the response type to land in, and the leak
tests plant sentinels in `data`/`stringData` to prove it.

The honest tradeoff: Kubernetes RBAC has no field-level reads, so key names
require `get`/`list` on whole Secrets. Response shaping protects against bugs
and careless exposure, but RBAC is the only layer that limits what a fully
compromised porthole process (e.g. a malicious transitive dependency) could
exfiltrate with its ServiceAccount token - and this grant raises that worst
case from "hygiene failures in pod specs" to "all secret values in bound
namespaces". That is why these tools are off by default, behind their own flags
(`--enable-secret-metadata`, `--enable-configmap-metadata`) and their own RBAC
object (`porthole-secret-metadata`) - a visible, deliberate, per-namespace
opt-in you can review and revoke independently of everything else. To keep it
at one RoleBinding per namespace, the aggregated
`porthole-readonly-with-secrets` ClusterRole unions the two roles via a
Kubernetes `aggregationRule`: bind it instead of `porthole-readonly` exactly
where you accept the tradeoff, and the roleRef name itself documents the
choice. Partial coverage exists without it: pod env `valueFrom` refs already
show expected key names, and mount failures name missing keys in events.

Response sizes are capped in the consumer's interest, at two levels.
`tail_lines` and `max_bytes` are per-request *tool parameters* on
`pod_logs` - the caller picks how much it wants. `--max-log-bytes` and
`--max-events` are server-side ceilings set by the operator: per-request
values are clamped to `--max-log-bytes`, and every event list (the `events`
tool and events inlined into other responses) is capped at `--max-events`.
Truncation is always marked explicitly in the response.

## Running it

### In-cluster (recommended)

Deploy under a dedicated ServiceAccount and run in HTTP mode
(`--mode=http`); the server speaks MCP streamable HTTP and serves
`/healthz` (unauthenticated) for probes. Reach it with
`kubectl port-forward` and register `http://localhost:<port>` as an MCP
server in your client.

The server is entirely stateless so it scales to any replica count
with no coordination or session affinity.

[`kustomize/`](kustomize/) contains a deployable Kustomize app in two variants, and an example overlay:
* `apps/porthole/base` is the least-permissioned posture
* `apps/porthole/full` adds the higher-privilege pieces to the base: the consolidated cluster-scoped grant (`porthole-cluster-readonly`), `pods/log`, the `resource_status` tool, and the Secret/ConfigMap metadata pieces.
* `overlays/prod/porthole` is an example overlay

The RBAC pattern: a ClusterRole is bound on a per-namespace basis via a
RoleBinding (deliberately no ClusterRoleBinding), so porthole can only read
from namespaces that have explicitly opted in - each namespace binds
either `porthole-readonly` or, to also opt into Secret/ConfigMap key-name
metadata, the aggregated `porthole-readonly-with-secrets`. Keep the
RoleBindings in lockstep with the `namespaces` allowlist config, and
enumerate the CRD API groups your cluster actually runs in the ClusterRole.

The one exception is `porthole-cluster-readonly` (full variant): a single
deliberate ClusterRoleBinding consolidating everything that cannot be bound
per-namespace - PersistentVolumes, Namespaces, Nodes and node metrics,
StorageClasses, webhook configurations, ClusterRoles/ClusterRoleBindings,
and any cluster-scoped CRD kinds you list in `--cluster-kinds`. Every
feature it powers degrades gracefully without it: `pvc_status` still
returns full PVC info, `rbac_summary` reports ClusterRole refs unresolved,
and so on.

### Local, against a kubeconfig

The same binary runs locally over stdio (`--mode=stdio`, the default)
using a kubeconfig (`--kubeconfig`, falling back to in-cluster config and
then client-go's default loading rules).

Using your admin kubeconfig is fine for testing, but every query would be
attributed to you in the audit log, so for long-term local use you probably
want to instead build a kubeconfig bound to a dedicated read-only
ServiceAccount with a short-lived token.

Stdio MCP servers are launched by the client, so configure your MCP client
with the porthole command and flags (no config file needed). For example,
with Claude Code:

```sh
claude mcp add porthole -- porthole --kubeconfig=./ai-readonly.kubeconfig --namespaces=team-a,team-b
```

## Configuration

YAML config file (`--config`) plus flags; flags override the file. Feature
gates default conservative.

`--config` also accepts a directory: each file becomes one top-level
config key, its content parsed as that key's YAML value. This is the shape
of a mounted ConfigMap (one `data` key per config field), which keeps the
config mergeable per-key with kustomize; files named `views-<suffix>`
concatenate into the `views` list, so overlays *append* views instead of
overriding the base's. Views declaring the same group and kind merge their
facts, and a fact reusing an earlier name overrides it (last wins). Pair it with a kustomize `configMapGenerator` and
any config change rolls the Deployment automatically (the generated name
carries a content hash). The example kustomize config
([base](kustomize/apps/porthole/base) →
[overlay](kustomize/overlays/prod/porthole)) demonstrates the whole
pattern.

| Flag | Default | Purpose |
|---|---|---|
| `--config` | - | Path to a YAML config file, or a directory of per-key files (see above) |
| `--mode` | `stdio` | `http` (streamable HTTP) or `stdio` |
| `--listen-addr` | `:8080` | HTTP bind address |
| `--kubeconfig` | - | Kubeconfig path (default: in-cluster, then client-go loading rules) |
| `--namespaces` | - | Comma-separated allowlist; empty = whatever RBAC allows (RBAC is the real boundary) |
| `--enable-crd-conditions` | `true` | `resource_conditions` tool |
| `--enable-crd-status` | `false` | `resource_status` tool (full `.status` of arbitrary kinds) |
| `--enable-logs` | `false` | `pod_logs` tool |
| `--enable-secret-metadata` | `false` | `secret_metadata` tool (key names, never values; see the tradeoff section) |
| `--enable-configmap-metadata` | `false` | `configmap_metadata` tool |
| `--allowed-annotations` | - | Comma-separated exact annotation keys to include in responses (`last-applied` always denied) |
| `--cluster-kinds` | - | Comma-separated cluster-scoped kinds the generic tools may serve, e.g. `ClusterIssuer` |
| `--max-log-bytes` | `262144` | Server-side ceiling on log response bytes |
| `--max-events` | `100` | Server-side ceiling on events per response |
| `--auth-token-file` | - | Require `Authorization: Bearer <token>` in HTTP mode (defense in depth) |

`views` (declarative per-kind facts) have structure and are configured in
the YAML config file only - see the Views section above.

The MCP `instructions` string advertises the namespace allowlist and
enabled features, so clients know the terrain without probing.

## Development

With Nix + direnv: `direnv allow`,
or `nix develop`. Without: any Go ≥ 1.26.

```sh
go test ./...      # unit + leak + strip + protocol tests
nix flake check    # build + tests + golangci-lint
nix build          # static binary at ./result/bin/porthole
```

The flake also exports `overlays.default` and `packages.<system>.porthole`
for consumption as a flake input (Linux and macOS, x86_64 and aarch64).

## License

Apache-2.0
