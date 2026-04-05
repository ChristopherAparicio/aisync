# Project Setup & Onboarding — Design Spec

> Goal: Make it dead simple to set up any project in aisync.  
> A developer discovering aisync should go from `install` to `fully configured` in under 2 minutes.

---

## Problem

Today, projects in aisync are **implicit** — they appear when sessions are captured.
There is no way to:
- Register a project explicitly
- Configure git/PR settings per project
- Auto-detect and propose smart defaults
- Do it all from a single CLI command

The result: a new user has to manually edit config.json or navigate 3+ web UI sections
to get a project properly configured. There are 36 CLI commands but none for `project`.

---

## Design Principles

1. **Zero-config for basics** — capturing sessions works with `aisync setup`, nothing else
2. **One command for full setup** — `aisync project init` configures everything for a project
3. **Auto-detect everything** — git remote, branches, platform, project type, existing sessions
4. **Explicit is better** — auto-detected values are shown and confirmed, not silently applied
5. **Two modes always** — interactive wizard (default) and flags for CI/scripting
6. **Per-project, not global** — git branch, PR tracking, budget are project-level settings

---

## Architecture

### Config: enrich `ProjectClassifierConf`

```go
type ProjectClassifierConf struct {
    // --- Existing fields ---
    TicketPattern string
    TicketSource  string
    TicketURL     string
    BranchRules   map[string]string
    AgentRules    map[string]string
    CommitRules   map[string]string
    StatusRules   map[string]string
    Tags          []string
    Budget        *ProjectBudgetConf

    // --- NEW: Git & Platform ---
    DefaultBranch string `json:"default_branch,omitempty"` // reference branch (main, dev, master)
    PREnabled     bool   `json:"pr_enabled,omitempty"`     // enable PR sync for this project
    GitRemote     string `json:"git_remote,omitempty"`     // normalized remote (github.com/org/repo)
    Platform      string `json:"platform,omitempty"`       // "github", "gitlab", "" (auto from remote)
    ProjectPath   string `json:"project_path,omitempty"`   // local filesystem path to the repo
}
```

Key insight: `projects` map in config is already keyed by display name (e.g. `"org/repo"`).
The new fields just add git-awareness to an existing structure. No new tables, no new interfaces.

---

## CLI: `aisync project` command group

| Command | Purpose |
|---------|---------|
| `aisync project init` | Interactive wizard — configure current project |
| `aisync project list` | List all known projects with status |
| `aisync project show` | Show config for current/named project |
| `aisync project sync-prs` | Manual PR sync for current project |

---

### `aisync project init` — The Wizard

**Interactive flow (default):**

```
$ cd ~/dev/cycloplan
$ aisync project init

  Scanning project...

  Directory:  /Users/chris/dev/cycloplan
  Git remote: github.com/ChristopherAparicio/cycloplan
  Platform:   GitHub
  Branches:   main, dev, feature/api (3 total)

  ? Project name [cycloplan]: ▌
  ? Default branch [main]: ▌
  ? Track pull requests? [Y/n]: ▌
  ? Monthly budget USD (0 = none) [0]: ▌
  ? Tags (comma-separated) []: backend, api

  Project configured:
    Name:           cycloplan
    Default branch: main
    PR tracking:    enabled
    Git remote:     github.com/ChristopherAparicio/cycloplan
    Sessions:       47 already captured

  Saved to ~/.aisync/config.json
```

**Non-interactive mode (CI/scripting):**

```bash
aisync project init \
  --name cycloplan \
  --branch main \
  --pr-enabled \
  --budget 200 \
  --no-prompt
```

**Auto-detect logic:**
- `git remote get-url origin` → remote URL
- `git branch -r` → available branches
- `git symbolic-ref refs/remotes/origin/HEAD` → default branch
- Remote URL pattern → platform (github.com → GitHub, gitlab.com → GitLab)
- DB query → session count for this project path
- File heuristic → project type (package.json, go.mod, Cargo.toml, pyproject.toml)

---

### `aisync project list`

```
$ aisync project list

  PROJECT                    SESSIONS  BRANCH  PRS  BUDGET     STATUS
  cycloplan                  47        main    ✓    $200/mo    configured
  ChristopherAparicio/aisync 312       main    ✓    —          configured
  anomalyco/opencode         89        dev     —    —          unconfigured
  brainfm                    12        —       —    —          unconfigured

  4 projects (2 configured, 2 unconfigured)

  Tip: cd into an unconfigured project and run `aisync project init`
```

Data sources:
- `store.ListProjects()` → all projects with sessions (from DB)
- `config.GetAllProjectClassifiers()` → all configured projects
- Merge both, show "configured" vs "unconfigured"

---

### `aisync project show`

```
$ aisync project show

  Project: cycloplan
  Path:    /Users/chris/dev/cycloplan
  Remote:  github.com/ChristopherAparicio/cycloplan
  Platform: GitHub

  Branch:     main
  PR sync:    enabled (last sync: 2 hours ago)
  Sessions:   47 (last: 3 hours ago)
  Budget:     $45.20 / $200.00 this month (22%)

  Rules:
    Branch: feature/* → feature, fix/* → bug
    Tickets: CYCLO-\d+ (Jira)
    Tags: backend, api
```

Runs from current directory or `--name cycloplan`.

---

### PRSyncTask — Multi-repo

Current: uses a single global `Platform()` (one repo only).

New: iterates over all projects with `pr_enabled: true`:

```go
func (t *PRSyncTask) Run(ctx context.Context) error {
    classifiers := t.cfg.GetAllProjectClassifiers()
    for name, conf := range classifiers {
        if !conf.PREnabled || conf.ProjectPath == "" {
            continue
        }
        plat := github.New(conf.ProjectPath)
        if !plat.Available() {
            t.logger.Printf("[pr_sync] %s: gh not available, skipping", name)
            continue
        }
        prs, err := plat.ListRecentPRs("all", 100)
        if err != nil {
            t.logger.Printf("[pr_sync] %s: %v", name, err)
            continue
        }
        for _, pr := range prs {
            t.store.SavePullRequest(&pr)
            // Link sessions by branch match
            sessions, _ := t.store.List(session.ListOptions{Branch: pr.Branch})
            for _, s := range sessions {
                t.store.LinkSessionPR(s.ID, pr.RepoOwner, pr.RepoName, pr.Number)
            }
        }
    }
    return nil
}
```

Dependencies change: `PRSyncTask` needs `config.Config` instead of `platform.Platform`.

---

## Implementation Steps

| # | Task | Effort | Files |
|---|------|--------|-------|
| 1 | Config: add fields to `ProjectClassifierConf` | S | `config.go` |
| 2 | Config: Get/Set/loadFrom for new keys | S | `config.go` |
| 3 | Config: getter methods + validation | S | `config.go` |
| 4 | Config: tests | S | `config_test.go` |
| 5 | CLI: `project` command group scaffold | S | `projectcmd/projectcmd.go` |
| 6 | CLI: `project list` | M | `projectcmd/list.go` |
| 7 | CLI: `project init` auto-detect | M | `projectcmd/init.go` |
| 8 | CLI: `project init` interactive prompts | M | `projectcmd/init.go` |
| 9 | CLI: `project init` non-interactive flags | S | `projectcmd/init.go` |
| 10 | CLI: `project show` | S | `projectcmd/show.go` |
| 11 | CLI: `project sync-prs` | S | `projectcmd/syncprs.go` |
| 12 | Register `project` in root.go | S | `root.go` |
| 13 | PRSyncTask: refactor to multi-repo | M | `prsync_task.go` |
| 14 | PRSyncTask: tests | M | `prsync_task_test.go` |
| 15 | End-to-end: init + sync + verify | S | manual test |

S = small (<30 min), M = medium (30-60 min)

**Total estimated: ~5-6 hours**

---

## Files to create

```
pkg/cmd/projectcmd/
  projectcmd.go   — cobra command group + register subcommands
  init.go         — wizard: auto-detect + prompts + save config
  list.go         — list projects (DB + config merge)
  show.go         — show project details
  syncprs.go      — manual PR sync
```

## Files to modify

```
internal/config/config.go       — new fields, Get/Set, loadFrom merge
internal/config/config_test.go  — tests for new fields
internal/scheduler/prsync_task.go      — multi-repo iteration
internal/scheduler/prsync_task_test.go — updated tests
pkg/cmd/root/root.go            — register projectcmd
```

---

## Open questions

1. **Project name key**: Use `org/repo` (from remote) or folder basename? Today it's `org/repo` for classifiers.
   → Proposal: auto-detect `org/repo` from remote, fallback to basename. User can override.

2. **Multiple paths for same remote**: e.g. worktrees. One remote = one project, path is just "where I work from".
   → Store `project_path` as the primary path, worktrees are transparent.

3. **Remote server mode**: `aisync project init --server http://aisync:8371` — agent mode where captures are forwarded.
   → Phase 2. For now, local only. The `server.url` config already exists.
