---
name: remediate-capz-findings
description: Use when triaging or remediating the open Trivy and govulncheck GitHub issues in stolostron/cluster-api-provider-azure across release and backplane branches, including repeat runs after PRs or scans change.
---

# Remediate CAPZ findings

Run this workflow for `stolostron/cluster-api-provider-azure`. Use `gh` for all GitHub reads and writes; never use a browser for GitHub. Treat every run as a reconciliation of current issues, PRs, branches, and scanner results. Print the start and finish slash-command banners required by the user's AGENTS.md.

## Entry check

First, list all open issues in the downstream repo, paging past the default limit. If there are none, report `No open issues to process`, print the finish banner, and stop. Do not inspect scans or PRs or make GitHub changes on an empty run.

## Discover current state

1. Read `docs/VULNERABILITY-MANAGEMENT.md` on downstream `main`, especially **Branch coverage**, and `.github/workflows/upstream-sync.yml` plus the active fast-forward workflows. Derive current remediation sources and upstream branch targets each run. Currently `main` feeds `backplane-5.1`/`backplane-5.2`, `release-1.26` feeds `backplane-5.0`, and `release-1.23`, `backplane-2.11`, and `backplane-2.17` are separate downstream targets. The upstream sync workflow maps `backplane-2.11` and `backplane-2.17` to upstream `release-1.22`; the other source branches map to like-named branches in `kubernetes-sigs/cluster-api-provider-azure`. If the live workflows differ from the document, inspect the discrepancy before acting.
2. Use the open issue list from the entry check. Process the GHA security findings; report any unrelated open issue as outside this workflow so none silently disappear. Include issues assigned to `RadekCap` or unassigned; skip any assigned to someone else and report the skip. Assign each processed unassigned issue to `RadekCap`. Re-read an issue immediately before a write so a new assignee or a bot closure is respected.
3. Identify branch and checker from labels and the original message. Inspect the latest completed scheduled or manually dispatched scan of that branch by the same checker for current findings, scan time, and run URL; use issue comments to locate and corroborate scans, but do not treat an older detection comment or successful run as current. A govulncheck job fails for reachable vulnerabilities as well as scan errors: use the matching `govulncheck-results-<branch>` artifact's `scan-result.json` to distinguish `vulnerable`, `clean`, and `error`. If a govulncheck issue comment truncates the findings, read the full `govulncheck-output.txt` from that artifact. Titles and initial issue bodies are historical: for example, issue #531's title says 12 while its 2026-11-12 comment reports 2. If the latest scan or its evidence is unavailable or unclear, mark the issue as needing evidence; do not substitute an old count.
4. Extract findings individually using vulnerability ID (CVE/GO), package or module, affected version, and file or package path where available. Counts alone do not establish equivalence. Compare findings across source and fast-forward branches, and across Trivy and govulncheck where they overlap. Record partial overlap or mismatches explicitly; do not claim inheritance solely from a shared count.

## Decide remediation per finding

1. Read prior human comments, linked PRs, commits, and current PR states. Search open and merged MintMaker security PRs, upstream sync PRs, and human PRs for the same finding and target branch. A comment is history; verify its links against live PR and branch state. Do not open duplicate PRs or repeat unchanged comments.
2. For a finding on a fast-forward branch, use its actual source branch as the downstream remediation target and link the source branch issue/PR from the inherited issue. Do not create a separate PR against the fast-forward branch. If the finding sets differ, investigate the branch state and report the exception.
3. Check the corresponding branch in `kubernetes-sigs/cluster-api-provider-azure` for a fix. If it is fixed upstream, port or sync that fix to the downstream source branch. If it is not fixed upstream, prepare coordinated upstream and downstream fixes. Reuse an existing suitable PR when possible; inspect whether it actually covers the finding and target branch. Handle multiple findings in one issue separately, grouping compatible fixes only when the resulting PR remains clear.
4. For changes that require code, use a feature branch, preserve the repository's attribution policy, and review the concrete diff. Before `gh pr create`, explicitly state the target `owner/repo`, base branch, and proposed change; follow the user's AGENTS.md rule requiring explicit approval for that PR. Pass `--repo owner/repo` to every `gh pr create` invocation. If approval or a fix is pending, report the prepared state and continue with independent issues.

## Update and verify

- On first handling, comment on the source issue with finding identity, source branch, upstream/downstream fix or PR links, and next step. Comment on inherited issues with their source branch and fix links. Add later comments only for a material status change: a PR opens or merges, remediation changes, a checker clears or still reports after a fix, or an issue closes. Include links and dates. Avoid a new comment for an unchanged daily scan.
- When a downstream fix merges, inspect a successful **post-merge run of the same checker on each affected branch**. Trigger its manual GHA workflow if needed and supported; wait for its result. If any finding remains, leave the issue open and report the remaining IDs. Close an issue with a status comment only when that checker no longer reports any of its findings on that branch. If the bot already closed it, report that state without duplicating the action.
- If an open PR exists, report its live status and continue; do not start the same work again. If a merged PR has not yet been followed by a successful checker run, report verification pending. If a run fails, preserve the issue and report the failure.

## Final output

Return a short table with one row for each current remediation source, ordered `main`, `release-1.26`, `release-1.23`, `backplane-2.11`, `backplane-2.17`, then any newly active source from the live branch map. Columns: **Source branch**, **Current findings / inherited branches**, **Action and status**, **PRs**, **Issue comments / closures**. Use direct links. Write `No changes needed` where appropriate. Below the table, list only skipped issues, finding mismatches, unavailable evidence, or actions waiting for approval. Distinguish work completed in this run from existing PRs and prior comments.
