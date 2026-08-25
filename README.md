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
| `list_workloads` | "What's unhealthy here/anywhere?" | Deployments/StatefulSets/DaemonSets: replica counts, conditions. Omit `namespace` to fan out over the allowlist |
| `workload_status` | "Why isn't this rolling out?" | Conditions, observedGeneration vs generation, rollout revision, owned-pod summaries |
| `list_pods` | "What state are the pods in?" | Phase, ready x/y, restarts, node, per-container state reasons + exit codes |
| `pod_status` | Safe `kubectl describe pod` | Container states incl. last termination, conditions, QoS, images, env **names only**, volume names+types, resources, ports, events |
| `events` | "What happened?" | Filter by object and/or time window |
| `service_endpoints` | "Why is nothing answering?" | Selector, ports, ready/not-ready endpoint counts from EndpointSlices |
| `pvc_status` | "Why is the pod stuck Pending?" | PVC phase, size, storage class; bound PV detail; unbound PVs still referencing the namespace. PV detail needs the optional cluster-scoped grant and degrades gracefully without it |
| `job_status` | "Did the job/cron run?" | Job counts/conditions, CronJob schedule + last run times |
| `resource_conditions` | Any kind's health, incl. CRDs | `.status.conditions` + events |
| `resource_status` | Deeper CRD inspection | Full `.status` + events. Flag-gated, off by default |
| `pod_logs` | Crash-loop diagnosis | `previous=true` for crashed containers, server-side RE2 `grep`, explicit truncation markers. Flag-gated, off by default |

Response sizes are capped in the consumer's interest, at two levels.
`tail_lines` and `max_bytes` are per-request *tool parameters* on
`pod_logs` — the caller picks how much it wants. `--max-log-bytes` and
`--max-events` are *server-side ceilings* set by the operator: per-request
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
* `apps/porthole/full` adds the higher-privilege pieces to the base including cluster-scoped PersistentVolume read, `pods/log`, and the `resource_status` tool.
* `overlays/prod/porthole` is an example overlay

The RBAC pattern: a ClusterRole is bound on a per-namespace basis via a
RoleBinding (deliberately no ClusterRoleBinding), so porthole can only read
from namespaces that have explicitly opted in. Keep the RoleBindings in
lockstep with the Deployment's `--namespaces` allowlist flag, and enumerate
the CRD API groups your cluster actually runs in the ClusterRole. The one exception
is PersistentVolumes (cluster-scoped, in the full variant); without that grant
`pvc_status` still returns full PVC info and notes the missing permission.

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

| Flag | Default | Purpose |
|---|---|---|
| `--config` | - | Path to YAML config file |
| `--mode` | `stdio` | `http` (streamable HTTP) or `stdio` |
| `--listen-addr` | `:8080` | HTTP bind address |
| `--kubeconfig` | - | Kubeconfig path (default: in-cluster, then client-go loading rules) |
| `--namespaces` | - | Comma-separated allowlist; empty = whatever RBAC allows (RBAC is the real boundary) |
| `--enable-crd-conditions` | `true` | `resource_conditions` tool |
| `--enable-crd-status` | `false` | `resource_status` tool (full `.status` of arbitrary kinds) |
| `--enable-logs` | `false` | `pod_logs` tool |
| `--max-log-bytes` | `262144` | Server-side ceiling on log response bytes |
| `--max-events` | `100` | Server-side ceiling on events per response |
| `--auth-token-file` | - | Require `Authorization: Bearer <token>` in HTTP mode (defense in depth) |

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
