# Downstream context (stolostron / ARO-HCP)

Entry point for AI coding agents (and new humans) working in the
**stolostron** fork of Cluster API Provider Azure (CAPZ).

This file is a **table of contents**, not a manual. It describes only what is
*specific to this fork* and points at the authoritative docs for everything
else — it does not duplicate them. Read the linked document for detail before
making changes.

> **This is a downstream fork.** The engineering source of truth — architecture,
> build/test commands, controller patterns, code generation — lives in the
> upstream-maintained docs in this repo. Start with
> [`AGENTS.md`](../AGENTS.md) (the full CAPZ agent manual, synced from upstream)
> and [`README.md`](../README.md). This page adds only the stolostron/ARO-HCP
> layer on top.

## What this fork is

`stolostron/cluster-api-provider-azure` is a downstream fork of the upstream
project [`kubernetes-sigs/cluster-api-provider-azure`](https://github.com/kubernetes-sigs/cluster-api-provider-azure).
It packages CAPZ for **Azure Red Hat OpenShift Hosted Control Planes (ARO-HCP)**
and ships it as part of **multicluster engine (MCE) / backplane** via
[Konflux](https://konflux-ci.dev/) builds.

The Go module path stays `sigs.k8s.io/cluster-api-provider-azure` — this fork
does not rename the module.

## How it differs from upstream

The Go source is kept as close to upstream as possible; the downstream-only
pieces are the release-branch model, the automated upstream sync, and the
Konflux build/pipeline configuration.

| Concern | Where it lives | Notes |
|---------|----------------|-------|
| Automated upstream sync | [`.github/workflows/upstream-sync.yml`](../.github/workflows/upstream-sync.yml) | Weekly (Mon 08:17 UTC) + manual dispatch. Opens draft PRs that merge upstream release branches into the downstream branches. |
| Konflux build pipelines | [`.tekton/`](../.tekton) | Tekton `PipelineRun` definitions for MCE releases (`mce-217`, `mce-51`, `mce-50`), split into `-pull-request` and `-push` variants. |
| Release branches | see sync matrix below | Downstream branches track specific upstream release branches. |
| Dependency update PRs | [`renovate.json`](../renovate.json) | Raised by Konflux **MintMaker**, not by Dependabot. See [Dependency updates](#dependency-updates) below. |

### Branch model

| Downstream branch | Tracks upstream | Ships in |
|-------------------|-----------------|----------|
| `main` | `main` | MCE 5.1 dev; PRs also trigger the `mce-51` Konflux build |
| `backplane-5.1` | `main` (fast-forwarded from `main` by [`ffwd-branch.yaml`](../.github/workflows/ffwd-branch.yaml)) | MCE 5.1 |
| `backplane-5.0` | `release-1.26` (fast-forwarded from `release-1.26`) | MCE 5.0 |
| `backplane-2.17` | `release-1.22` | MCE 2.17 |
| `backplane-2.11` | `release-1.22` | MCE 2.11 |

Sync-only branches receive upstream syncs but are not development targets:

- `release-1.26` is fast-forwarded into `backplane-5.0` (the MCE 5.0 shipping
  branch) by [`ffwd-branch.yaml`](../.github/workflows/ffwd-branch.yaml) on push.
- `release-1.23` is synced from upstream to feed a downstream ACM build whose
  pipeline is defined outside this repo; it has no in-repo Konflux pipeline or
  shipping branch here.

The authoritative matrix is the `strategy.matrix` in
[`upstream-sync.yml`](../.github/workflows/upstream-sync.yml) — consult it there
rather than trusting this table if they disagree.

### Working with the fork

- **Keep changes upstream-friendly.** Prefer contributing fixes upstream and
  letting them flow down via the sync. Downstream-only patches make every future
  sync harder to merge.
- **Do not edit upstream-tracked files to add downstream notes.** Files such as
  [`AGENTS.md`](../AGENTS.md), [`CLAUDE.md`](../CLAUDE.md), [`README.md`](../README.md),
  and [`CONTRIBUTING.md`](../CONTRIBUTING.md) are synced from upstream; edits to
  them create recurring merge conflicts. Put downstream-specific context here
  instead.
- **PR target.** Open PRs against the appropriate downstream branch
  (`main` / `backplane-5.1` / `backplane-5.0` / `backplane-2.17` / `backplane-2.11`),
  not against upstream.

## Dependency updates

Scanning and update-PR creation are separate concerns here, handled by
different tools:

| Job | Tool | Where |
|-----|------|-------|
| Vulnerability scanning | Trivy | [`daily-security-scan.yaml`](../.github/workflows/daily-security-scan.yaml) — daily across every maintained branch, results to the GitHub Security tab |
| Go vulnerability scanning (reachability) | govulncheck | [`govulncheck.yaml`](../.github/workflows/govulncheck.yaml) |
| Vulnerability *alerts* | GitHub Dependabot alerts + OSV | Alerts stay **enabled**; Renovate consumes them. OSV is what covers the non-default branches, where Dependabot alerts don't reach. |
| Update *pull requests* | Konflux **MintMaker** | Configured by [`renovate.json`](../renovate.json) |

Dependabot's own PR features are deliberately **off** (no `.github/dependabot.yml`,
"Dependabot security updates" disabled in repository settings) because MintMaker
supersedes them. Do not re-enable them — you get duplicate PRs. Keep the
dependency graph and Dependabot *alerts* on; Renovate needs them.

The policy in `renovate.json` is **vulnerability-only**: routine version and
digest bumps are disabled, and a known vulnerability with an available fix
produces one grouped `Security fixes` PR per base branch.

### MintMaker overrides repository `packageRules` — read this before editing `renovate.json`

MintMaker *is* Renovate, run as a hosted service by Konflux. It applies its own
[global config](https://github.com/konflux-ci/mintmaker/blob/main/config/renovate/renovate.json)
underneath this repository's `renovate.json`, and parts of it live inside
manager-scoped objects — notably:

```json
"gomod": {
  "packageRules": [
    { "matchManagers": ["gomod"], "matchDepTypes": ["indirect"], "enabled": true }
  ]
}
```

Renovate merges manager-scoped `packageRules` **after** top-level ones, and the
last matching rule wins. So a top-level rule in this repo's config **cannot**
override a MintMaker rule of the same kind — the override has to sit at the same
specificity, inside a top-level `"gomod"` / `"tekton"` / `"dockerfile"` object.
This is why `renovate.json` carries a `gomod.packageRules` entry: without it
MintMaker re-enables every `// indirect` module, which both floods the repo with
single-dependency PRs and silently defeats the coordinated-upgrade pins for
`k8s.io/*`, OpenTelemetry, `sigs.k8s.io/cloud-provider-azure` and Prometheus
(all of which are indirect in `go.mod`).

Vulnerability alerts force-override these `enabled: false` rules, so security
fixes still land.

### Validating a `renovate.json` change

Schema validation alone is not enough — it will not catch a rule that MintMaker
overrides. Reproduce the real merge by running Renovate against MintMaker's
global config and reading the `Returning N branch(es)` line:

```sh
mkdir -p /tmp/rnv && cp go.mod go.sum renovate.json /tmp/rnv/
curl -sL https://raw.githubusercontent.com/konflux-ci/mintmaker/main/config/renovate/renovate.json \
  -o /tmp/rnv/global.json
# edit /tmp/rnv/global.json: set "platform": "local", "dryRun": "full",
# drop the Konflux-only keys (platformCommit, inheritConfig, autodiscover,
# forkProcessing, allowedCommands, rpm* and the "rpm-lockfile" manager)
podman run --rm -v /tmp/rnv:/usr/src/app:Z -w /usr/src/app \
  -e RENOVATE_CONFIG_FILE=/usr/src/app/global.json -e LOG_LEVEL=debug \
  -e GITHUB_COM_TOKEN="$(gh auth token)" \
  ghcr.io/renovatebot/renovate:latest 2>&1 | grep -E 'Returning [0-9]+ branch'
```

Note that the `packageFiles with updates` debug dump lists *pre-filter* lookup
results and will show candidates even for disabled dependencies — only
`Returning N branch(es)` reflects what MintMaker would actually open.

There is intentionally **no** Renovate GitHub Actions workflow in this repo. One
existed and was removed: `GITHUB_TOKEN` cannot read Dependabot alerts and cannot
write to `.github/workflows/`, so it could never reproduce MintMaker's
behaviour — it only provided a second config surface that gave misleading
dry-run results.

## Start here

| Document | What it covers |
|----------|----------------|
| [AGENTS.md](../AGENTS.md) | **Upstream CAPZ agent manual** — architecture, dev/build/test commands, controller & service patterns, code generation, testing strategy. The primary source of truth. |
| [CLAUDE.md](../CLAUDE.md) | Claude Code entry point (defers to `AGENTS.md`). |
| [README.md](../README.md) | Project overview, prerequisites, quick start. |
| [CONTRIBUTING.md](../CONTRIBUTING.md) | Upstream contribution workflow and conventions. |
| [docs/README.md](README.md) | Documentation index (links into the CAPZ book at capz.sigs.k8s.io). |
| [SECURITY.md](../SECURITY.md) / [SUPPORT.md](../SUPPORT.md) | Vulnerability reporting and support channels. |
