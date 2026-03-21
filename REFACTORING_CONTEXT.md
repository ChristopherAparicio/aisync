# AiSync — Contexte de Refactoring (Session Persistante)

**Dernière mise à jour :** 2026-03-16 (Phase 7 complétée)

## État du Projet

- **Architecture** : Hexagonale (Ports & Adapters)
- **Build** : ✅ OK (tous les packages compilent)
- **Tests** : ✅ 62 packages OK, 0 FAIL
- **Couverture** : ~61% globale (très inégale selon les packages)

## Refactorings Complétés

### ✅ Phase 0.1 : Mock Store centralisé
- **Fichier créé** : `internal/testutil/mock_store.go`
- **Fichiers migrés** : 13 sur 14 (tous sauf `internal/service/session_test.go` — package interne)
- **Résultat** : ~350 lignes de boilerplate éliminées
- Le mock `internal/service/session_test.go` reste local car c'est un test white-box (`package service`, pas `package service_test`)

### ✅ Phase 0.2 : Split session.go
- **Avant** : 1 fichier de 2777 lignes
- **Après** : 17 fichiers de 60-437 lignes
- Fichiers créés dans `internal/service/` :
  - `session.go` (137 lignes) — struct, config, constructeur, helpers partagés
  - `session_capture.go` (167) — Capture, CaptureAll, CaptureByID
  - `session_restore.go` (66) — Restore
  - `session_crud.go` (146) — Get, List, ListTree, Delete
  - `session_export.go` (197) — Export, Import
  - `session_link.go` (219) — Link, Comment
  - `session_stats.go` (406) — Stats, EstimateCost, ToolUsage
  - `session_search.go` (93) — Search
  - `session_voice.go` (144) — sanitizeForVoice et helpers
  - `session_blame.go` (68) — Blame
  - `session_ai.go` (437) — Summarize, Explain, AnalyzeEfficiency, Rewind
  - `session_llm.go` (76) — buildSessionTranscript, truncate
  - `session_gc.go` (135) — GarbageCollect
  - `session_diff.go` (190) — Diff et helpers
  - `session_offtopic.go` (184) — DetectOffTopic
  - `session_forecast.go` (260) — Forecast
  - `session_ingest.go` (203) — Ingest, SessionLink

### ✅ Phase 1 : ISP sur Store
- **Avant** : 1 interface monolithique `Store` avec 28 méthodes
- **Après** : 7 interfaces de rôle + 1 interface composée
- **Approche** : Additive (zéro breaking change)
- **Fichier modifié** : `internal/storage/store.go`

**Interfaces créées :**

| Interface | Méthodes | Consommateurs typiques |
|---|---|---|
| `SessionReader` | Get, GetLatestByBranch, CountByBranch, List, GetFreshness | capture, restore, stats, gc, forecast, offtopic, status |
| `SessionWriter` | Save, Delete, DeleteOlderThan | capture, export, ai (Rewind), ingest, gc |
| `LinkStore` | AddLink, GetByLink, LinkSessions, GetLinkedSessions, DeleteSessionLink | link, comment, crud, restore, ingest |
| `UserStore` | SaveUser, GetUser, GetUserByEmail | session.go (resolveOwner) |
| `SearchStore` | Search, GetSessionsByFile | search, blame, registry |
| `AnalysisStore` | SaveAnalysis, GetAnalysis, GetAnalysisBySession, ListAnalyses | analysis.go |
| `CacheStore` | GetCache, SetCache, InvalidateCache, GetPreferences, SavePreferences | web/handlers.go |
| `Store` | toutes les interfaces ci-dessus + Close() | composition root (factory) |

**État actuel** : Tous les consommateurs utilisent encore `Store` directement. La migration vers les sous-interfaces est optionnelle et progressive.

### Méthodes Jamais Appelées en Prod
- `GetUser` — définie mais aucun consommateur
- `InvalidateCache` — définie mais aucun consommateur
- `SavePreferences` — définie mais aucun consommateur

### Matrice de Dépendances (qui appelle quoi sur Store)

| Consommateur | Méthodes Store utilisées |
|---|---|
| `service/session.go` (resolveOwner) | Get, GetLatestByBranch, SaveUser, GetUserByEmail |
| `service/session_capture.go` | Save |
| `service/session_restore.go` | GetByLink |
| `service/session_crud.go` | Get, List, Delete, GetByLink |
| `service/session_export.go` | Save |
| `service/session_link.go` | Get, GetLatestByBranch, AddLink, GetByLink |
| `service/session_search.go` | Search |
| `service/session_stats.go` | Get, List |
| `service/session_blame.go` | GetSessionsByFile |
| `service/session_gc.go` | Get, List, Delete, DeleteOlderThan |
| `service/session_ai.go` | Save (Rewind), Get |
| `service/session_ingest.go` | Save, LinkSessions, GetLinkedSessions, DeleteSessionLink |
| `service/session_forecast.go` | Get, List |
| `service/session_offtopic.go` | Get, List |
| `service/analysis.go` | Get, SaveAnalysis, GetAnalysis, GetAnalysisBySession, ListAnalyses |
| `service/registry.go` | Search |
| `capture/service.go` | Save, GetFreshness |
| `restore/service.go` | Get, GetLatestByBranch |
| `gitsync/service.go` | Save, Get, List |
| `web/handlers.go` | GetCache, SetCache, GetPreferences |
| `pkg/cmd/status/status.go` | GetLatestByBranch, CountByBranch |
| `pkg/cmd/factory/default.go` | Close |

### Consommateurs Indirects (via SessionServicer, pas Store)
- `internal/api/` — utilise `service.SessionServicer`
- `internal/mcp/` — utilise `service.SessionServicer`
- Tous les `pkg/cmd/*/` — utilisent `service.SessionServicer`

### ✅ Phase 2 : ISP sur SessionServicer
- **Avant** : 1 interface monolithique `SessionServicer` avec 28 méthodes
- **Après** : 9 interfaces de rôle + 1 interface composée
- **Approche** : Additive (zéro breaking change)
- **Fichier modifié** : `internal/service/iface.go`

**Interfaces créées :**

| Interface | Méthodes | Consommateurs typiques |
|---|---|---|
| `SessionCapturer` | Capture, CaptureAll, CaptureByID | CLI capture, API, MCP |
| `SessionRestorer` | Restore | CLI restore/resume, API, MCP |
| `SessionCRUD` | Get, List, ListTree, Delete | CLI show/list, API, MCP, Web |
| `SessionExporter` | Export, Import | CLI export/import, API, MCP |
| `SessionLinker` | Link, Comment | CLI link/comment, API, MCP |
| `SessionAnalytics` | Stats, Search, Blame, EstimateCost, ToolUsage, Forecast | CLI stats/search/blame, API, MCP, Web |
| `SessionAI` | Summarize, Explain, AnalyzeEfficiency | CLI explain/efficiency, API, MCP |
| `SessionManager` | Rewind, GarbageCollect, Diff, DetectOffTopic | CLI gc/diff/rewind, API, MCP |
| `SessionIngester` | Ingest, LinkSessions, GetLinkedSessions, DeleteSessionLink | API uniquement |
| `SessionServicer` | compose toutes les interfaces ci-dessus | composition root (factory) |

**Symétrie fichiers ↔ interfaces :**
- `SessionCapturer` → `session_capture.go`
- `SessionRestorer` → `session_restore.go`
- `SessionCRUD` → `session_crud.go`
- `SessionExporter` → `session_export.go`
- `SessionLinker` → `session_link.go`
- `SessionAnalytics` → `session_stats.go` + `session_search.go` + `session_blame.go` + `session_forecast.go`
- `SessionAI` → `session_ai.go`
- `SessionManager` → `session_gc.go` + `session_diff.go` + `session_offtopic.go` + `session_ai.go` (Rewind)
- `SessionIngester` → `session_ingest.go`

**Implémentations :** `*SessionService` (local) et `*remote.SessionService` satisfont toutes deux `SessionServicer`.

**Méthodes jamais appelées par un consommateur externe :**
- `Summarize` — appelée uniquement en interne par `Capture()`. Remote retourne erreur.
- `LinkSessions`, `GetLinkedSessions`, `DeleteSessionLink` — définies mais pas câblées.

### ✅ Phase 3 : Tests internal/config (7.8% → 97.6%)
- **Avant** : 58.6% (tests existants pour defaults, Set/Get basique, Dashboard)
- **Après** : 97.6% — 27/28 fonctions à 100%
- **Tests ajoutés** :
  - Get() : toutes les clés (summarize.*, analysis.*, server.url, database.path)
  - Set() : toutes les clés manquantes + tous les paths d'erreur de validation
  - Getters : IsSummarizeEnabled, GetSummarizeModel, GetPricingOverrides, IsAnalysisAutoEnabled, GetAnalysisAdapter, GetAnalysisErrorThreshold, GetAnalysisMinToolCalls, GetAnalysisModel, GetCustomPatterns, GetServerURL, GetDatabasePath
  - AddPricingOverride : append + update existing
  - AddCustomPattern
  - GetServerURL / GetDatabasePath : env var override (`AISYNC_SERVER_URL`, `AISYNC_DATABASE_PATH`)
  - loadFrom merge : Server, Database, Analysis, Pricing, Summarize, Secrets (global + repo)
  - New() edge cases : malformed JSON (global + repo), empty dirs
  - Save() : no dir specified, global fallback
  - Fallback defaults : zero-value → safe defaults pour tous les getters
  - GetStorageMode / GetSecretsMode : invalid value fallback
  - GetProviders : skips invalid entries
  - Save+Reload round-trip : all 18+ config keys + pricing overrides + custom patterns

### ✅ Phase 4 : Tests internal/storage/sqlite (59.4% → 82.0%)
- **Avant** : 59.4% (tests existants pour CRUD basique)
- **Après** : 82.0% — plus aucune fonction à 0%
- **Tests ajoutés** :
  - SaveAnalysis, GetAnalysis, GetAnalysisBySession, ListAnalyses (0% → 71-94%)
  - scanAnalysis, scanAnalysisRow (helpers internes couverts)
  - LinkSessions, GetLinkedSessions, DeleteSessionLink (0% → 71-94%)
  - GetFreshness (0% → couvert)

### ✅ Phase 5 : Tests pkg/cmdutil (0% → 100%)
- **Avant** : 0% (aucun fichier de test)
- **Après** : 100% — 13/13 méthodes couvertes
- **Tests ajoutés** :
  - 13 méthodes × 2-3 cas (nil func, success, error) = 38 tests
  - Config, Store, Git, Registry, Scanner, Platform, Converter, HooksManager
  - SessionService, SyncService, RegistryService, AnalysisService, Close
  - Zero-value Factory : vérifie qu'aucune méthode ne panique sur `&Factory{}`

### ✅ Phase 6 : Tests git/client.go (29.7% → 88.5%)
- **Avant** : 29.7% (14 fonctions à 0%)
- **Après** : 88.5% — 24/28 fonctions à 100%, 0 fonctions à 0%
- **Tests ajoutés** :
  - Checkout : success (restore file) + error (nonexistent file)
  - UserName/UserEmail : configured + unconfigured repo
  - HasRemote/RemoteURL : sans remote + avec remote
  - SyncBranchExists : false avant InitSyncBranch
  - InitSyncBranch + SyncBranchExists : cycle complet
  - WriteSyncFiles + ReadSyncFile : write/read round-trip, fichier inexistant, map vide
  - ListSyncFiles : sans branch, avec fichiers
  - PushSyncBranch/PullSyncBranch : sans remote (error), avec bare local remote (full cycle)
  - HooksPath : custom absolute + custom relative (core.hooksPath)
  - Error paths : CurrentBranch, TopLevel, HeadCommitSHA, CommitMessage, AddNote, HooksPath, HookExists sur non-repo

### ✅ Phase 8 : Fix examples/plugins/native (build cassé)
- **Problème** : `package main` sans `func main()` — le compilateur Go refusait de compiler en mode normal
- **Fix** : Ajout d'un `func main() {}` vide (requis par Go, jamais appelé en mode plugin)
- **Résultat** : `go build ./...` compile 100% sans erreur

### ✅ Phase 7 : Tests commandes CLI (16 packages à 0% → testés)
- **Avant** : 16 packages CLI sans aucun fichier de test
- **Après** : 16 packages avec tests, 0 FAIL, 62 packages total OK
- **Packages testés et couverture :**

| Package | Avant | Après | Tests |
|---|---|---|---|
| `gccmd` | 0% | 91.3% | flags, delete, dry-run, JSON, error |
| `diffcmd` | 0% | 80.3% | flags, text output, JSON, formatDelta, formatCostDelta |
| `explaincmd` | 0% | 66.7% | flags, service error, no LLM, invalid ID |
| `rewindcmd` | 0% | 88.9% | flags, success, JSON, error, out-of-range |
| `toolusagecmd` | 0% | 87.5% | flags, table, JSON, no tools, formatTokens, formatCost, truncate |
| `efficiencycmd` | 0% | 29.8% | flags, service error, no LLM |
| `resumecmd` | 0% | 86.8% | flags, git error, checkout, success, provider, session ID |
| `config` | 0% | 90.5% | subcommands, get/set/list, global flag, aliases |
| `analyzecmd` | 0% | 16.7% | flags, service error, empty ID |
| `agentscmd` | 0% | 26.2% | subcommands, aliases, flags, service errors, no projects |
| `root` | 0% | 91.5% | subcommands, version, completion bash, invalid shell |
| `mcpcmd` | 0% | 62.5% | use, service error, nil func |
| `servecmd` | 0% | 9.3% | use, flags (server is hard to unit test) |
| `tuicmd` | 0% | 22.2% | use, short (bubbletea is hard to unit test) |
| `webcmd` | 0% | 45.5% | use, addr flag, description |

- **Note** : Les packages AI (explaincmd, efficiencycmd, analyzecmd) ont une couverture plus basse car les chemins de succès nécessitent un LLM réel. Les packages serveur/TUI (servecmd, tuicmd) sont testés pour la création de commande uniquement.

### Matrice SessionServicer (qui appelle quoi)

| Consommateur | Méthodes utilisées |
|---|---|
| `pkg/cmd/capture` | Capture, CaptureAll, CaptureByID |
| `pkg/cmd/restore` | Restore, List |
| `pkg/cmd/resumecmd` | Restore |
| `pkg/cmd/listcmd` | List, ListTree, DetectOffTopic |
| `pkg/cmd/show` | Get, EstimateCost, ToolUsage, Blame |
| `pkg/cmd/export` | Export |
| `pkg/cmd/importcmd` | Import |
| `pkg/cmd/linkcmd` | Link |
| `pkg/cmd/commentcmd` | Comment |
| `pkg/cmd/searchcmd` | Search |
| `pkg/cmd/blamecmd` | Blame |
| `pkg/cmd/statscmd` | Stats, Forecast |
| `pkg/cmd/diffcmd` | Diff |
| `pkg/cmd/gccmd` | GarbageCollect |
| `pkg/cmd/toolusagecmd` | ToolUsage |
| `pkg/cmd/efficiencycmd` | AnalyzeEfficiency |
| `pkg/cmd/rewindcmd` | Rewind |
| `pkg/cmd/explaincmd` | Explain |
| `internal/api/handlers.go` | 22/28 méthodes (tout sauf CaptureAll, CaptureByID, ListTree, Summarize, LinkSessions, GetLinkedSessions, DeleteSessionLink) |
| `internal/mcp/tools.go` | 21/28 méthodes |
| `internal/web/handlers.go` | Stats, Forecast, List, Search, Get, ToolUsage, ListTree |

## Phases Suivantes (Roadmap)

| # | Phase | Priorité | Effort | État |
|---|-------|----------|--------|------|
| 1 | ISP sur `Store` (28 méthodes → 7 interfaces composées) | 🔴 Haute | ~2h | ✅ Fait |
| 2 | ISP sur `SessionServicer` (28 méthodes → 9 interfaces composées) | 🔴 Haute | ~1.5h | ✅ Fait |
| 3 | Tests `internal/config` (7.8% → 97.6%) | 🟡 Moyenne | ~30min | ✅ Fait |
| 4 | Tests `internal/storage/sqlite` (59.4% → 82.0%) | 🟡 Moyenne | ~2h | ✅ Fait |
| 5 | Tests `pkg/cmdutil` (0% → 100%) | 🟡 Moyenne | ~15min | ✅ Fait |
| 6 | Tests `git/client.go` (29.7% → 88.5%) | 🟢 Basse | ~30min | ✅ Fait |
| 7 | Tests commandes CLI (16 packages à 0%) | 🟢 Basse | ~4h | ✅ Fait |
| 8 | Fix `examples/plugins/native` (build cassé) | 🟢 Basse | ~5min | ✅ Fait |

## Fichiers Clés

- `internal/storage/store.go` — 7 interfaces de rôle + Store composé (post-ISP Phase 1)
- `internal/storage/sqlite/` — Implémentation SQLite (satisfait Store)
- `internal/service/iface.go` — 9 interfaces de rôle + SessionServicer composé (post-ISP Phase 2)
- `internal/service/session*.go` — 17 fichiers service (post-split Phase 0.2)
- `internal/service/remote/session.go` — Implémentation remote (satisfait SessionServicer)
- `internal/testutil/mock_store.go` — Mock centralisé (satisfait Store)
- `internal/testutil/testutil.go` — Helpers de test (InitTestRepo, MustOpenStore, NewSession)
- `pkg/cmd/factory/default.go` — Composition root (wiring DI)
- `pkg/cmdutil/factory.go` — Factory avec SessionServiceFunc lazy init
- `CONTRIBUTING.md` — Documentation architecture SOLID
