# AI5 — Attribution par agent et multi-fichiers pour `aisync blame`

## TL;DR

> **Quick Summary**: Enrichir `aisync blame` pour qu'un agent retrouve facilement des session IDs : ajouter **quel agent** a touché un fichier, accepter **plusieurs fichiers** en une commande, et offrir une **vue par projet** (« qu'est-ce qui a été modifié dans ce projet et par qui »).
>
> **Deliverables**:
> - `BlameEntry.Agent` exposé en table / `--json` / `--quiet` sur `aisync blame`
> - `aisync blame <f1> <f2> ...` multi-fichiers (clause `WHERE file_path IN (...)`)
> - `aisync blame --project <path>` : agrégation fichier → dernière session → agent
> - Outil MCP `aisync_blame` synchro : accepte `file` (string) ET `files` (array), renvoie l'agent
> - Couverture TDD complète (store, service, CLI, MCP) + QA agent-exécutée sur le binaire réel
>
> **Estimated Effort**: Medium
> **Parallel Execution**: YES - 5 waves
> **Critical Path**: Task 1 → Task 3 → Task 4 → Task 5/6 → Task 7 → F1-F4

---

## Context

### Original Request
Système multi-agent. Des agents ne sont « plus utilisés du tout » (idle). L'utilisateur veut qu'un agent puisse **contacter** un autre agent — mais la messagerie est déjà gérée par OpenCode (`Agent-chat` MCP, sessions actives uniquement). Le **vrai besoin recentré** : un agent doit pouvoir **retrouver facilement les session IDs** des sessions qui ont modifié des fichiers, et **savoir quel agent** les a modifiés, en interrogeant 1 fichier OU une liste de fichiers — axe principal = **par projet**.

### Interview Summary
**Key Discussions**:
- Livraison : feature **CLI AI5 d'abord** (doit fonctionner), un skill viendra ensuite (hors de CE plan).
- Adressage par **session ID** ; sortie agent-friendly (`--quiet` IDs bruts, `--json` détail).
- Ping / notify / réveil / auto-resume = **hors scope** (déjà géré ailleurs).
- Axe de découverte n°1 confirmé : **par projet** (« modifié dans ce projet et par qui »).
- Test strategy : **TDD** (RED → GREEN → REFACTOR).

**Research Findings**:
- `aisync blame` existe (`pkg/cmd/blamecmd/blamecmd.go`), git-indépendant (parse tool calls Edit/Write), mais `BlameEntry` **ne remonte pas l'agent** (présent sur `Session`/`sessions.agent`, absent du SELECT).
- `GetSessionsByFile` (`sqlite.go:1907`) fait déjà le JOIN `sessions ⋈ file_changes` mais ne SELECT pas `s.agent` ; clé sur un seul `file_path = ?`.
- La **vue par projet existe déjà au store + web** (`FilesForProject`, `sqlite.go:2148` ; `ProjectFileEntry`, `fileops.go:465`) mais **aucune commande CLI** ne l'expose, et elle **n'inclut pas l'agent**.
- L'outil MCP `aisync_blame` (`tools.go:339`, `server.go:171`) renvoie `BlameResult` → l'agent transitera automatiquement dès que `BlameEntry.Agent` est peuplé.

### Metis Review
**Identified Gaps** (addressed via défauts):
- Vue projet = nouveau mode **`--project <path>`** sur `blame` (pas de nouvelle commande).
- Dédup `--all=false` = per-file, **session la plus récente** par fichier.
- Agent vide = `COALESCE(s.agent,'')` → `""` en JSON / `"-"` en table (jamais drop la ligne).
- MCP accepte **`file` (string) ET `files` (array)** pour rétro-compat.
- **Task 1 (guard)** valide que `sessions.agent` est réellement peuplé et qu'il n'y a pas de N+1 sur le JOIN.

---

## Work Objectives

### Core Objective
Permettre à un agent de répondre à « quelles sessions (et quels agents) ont modifié ce(s) fichier(s) / ce projet ? » via `aisync blame`, en sortie consommable par agent (`--quiet`/`--json`).

### Concrete Deliverables
- `internal/session/session.go` : `BlameEntry.Agent`, `BlameQuery.FilePaths`
- `internal/session/fileops.go` : `ProjectFileEntry.LastAgent`
- `internal/storage/sqlite/sqlite.go` : `GetSessionsByFile` (agent + multi-fichiers), `FilesForProject` (agent)
- `internal/service/session_blame.go` : `BlameRequest.FilePaths`, logique multi-fichiers + mode projet
- `pkg/cmd/blamecmd/blamecmd.go` : multi-args, flag `--project`, colonne `AGENT`
- `internal/mcp/tools.go` + `internal/mcp/server.go` : param `files` (array) + `file` (string)
- Tests GO (TDD) à chaque couche + mise à jour `architecture/blame.md` + README

### Definition of Done
- [ ] `make test` → tous les tests passent (y compris les nouveaux)
- [ ] `make lint` → 0 issue
- [ ] `aisync blame <f>` affiche une colonne AGENT ; `--json` contient `"agent"`
- [ ] `aisync blame <f1> <f2>` agrège les deux fichiers
- [ ] `aisync blame --project <path>` liste fichier → dernière session → agent
- [ ] `aisync_blame` MCP accepte `files: [...]` et renvoie `agent`

### Must Have
- Champ `agent` dans toutes les sorties blame (table, `--json` ; `--quiet` reste IDs seuls).
- Multi-fichiers via arguments variadiques (`aisync blame a.go b.go`).
- Mode `--project <path>` agrégé par projet, incluant l'agent.
- Rétro-compat totale : `aisync blame <single-file>` et `aisync_blame {file}` inchangés en comportement.

### Must NOT Have (Guardrails)
- **PAS** de ping / messagerie / notify / réveil / auto-resume entre agents (géré par OpenCode `Agent-chat`).
- **PAS** de filtre `--agent` / `--status` sur `aisync list` (recentré hors scope cette fois).
- **PAS** de changement de schéma de la table `file_changes` (réutiliser l'existant + JOIN `sessions`).
- **PAS** de support glob/wildcard sur les chemins (liste explicite de fichiers uniquement).
- **PAS** de nouvelle commande CLI dédiée (réutiliser `blame` + flag `--project`).
- **PAS** de skill dans ce plan (livré séparément après la CLI).
- **PAS** de N+1 : l'agent doit venir du même JOIN, pas d'une requête par entrée.

---

## Verification Strategy (MANDATORY)

> **ZÉRO intervention humaine** — toute vérification est exécutée par l'agent.

### Test Decision
- **Infrastructure exists**: YES (Go `go test ./...`, suites existantes : `sqlite_test.go`, `session_test.go`, `server_test.go`)
- **Automated tests**: TDD (RED → GREEN → REFACTOR) à chaque tâche
- **Framework**: `go test` (table-driven, conforme aux suites existantes)

### QA Policy
Chaque tâche inclut des scénarios QA agent-exécutés sur le **binaire réel** (`make build` → `bin/aisync`) :
- **CLI**: `interactive_bash` (tmux) — seed une DB SQLite, lance `aisync blame ...`, vérifie stdout/exit code
- **MCP/Store/Service**: `Bash` — `go test` ciblé + `go run` ad hoc sur DB temporaire
Evidence dans `.omo/evidence/task-{N}-{scenario-slug}.{ext}`.

---

## Execution Strategy

### Parallel Execution Waves

```
Wave 1 (Start Immediately — types, fichiers distincts):
├── Task 1: session.go — BlameEntry.Agent + BlameQuery.FilePaths [quick]
└── Task 2: fileops.go — ProjectFileEntry.LastAgent [quick]

Wave 2 (After Wave 1 — couche store, sqlite.go sérialisé):
└── Task 3: GetSessionsByFile (agent + IN) + FilesForProject (agent) + iface/mock + tests [deep]

Wave 3 (After Wave 2 — couche service):
└── Task 4: session_blame.go — FilePaths + multi-fichiers + mode projet + tests [unspecified-high]

Wave 4 (After Wave 3 — adaptateurs, fichiers distincts, PARALLÈLE):
├── Task 5: blamecmd.go — multi-args + --project + colonne AGENT [unspecified-high]
└── Task 6: tools.go + server.go — param files[] + file + tests MCP [unspecified-high]

Wave 5 (After Wave 4 — docs):
└── Task 7: architecture/blame.md + README — agent/multi-fichiers/--project [writing]

Wave FINAL (After ALL — 4 revues parallèles, puis okay user):
├── Task F1: Audit conformité plan (oracle)
├── Task F2: Revue qualité code (unspecified-high)
├── Task F3: QA manuelle réelle (unspecified-high)
└── Task F4: Fidélité scope (deep)
-> Présenter résultats -> Obtenir okay explicite de l'utilisateur

Critical Path: Task 1 → Task 3 → Task 4 → Task 5/6 → Task 7 → F1-F4 → okay user
Max Concurrent: 2 (Waves 1 & 4)
```

### Dependency Matrix

- **1**: dépend de — | bloque 3
- **2**: dépend de — | bloque 3
- **3**: dépend de 1, 2 | bloque 4
- **4**: dépend de 3 | bloque 5, 6
- **5**: dépend de 4 | bloque 7
- **6**: dépend de 4 | bloque 7
- **7**: dépend de 5, 6 | bloque F1-F4
- **F1-F4**: dépend de 7 | bloque okay user

### Agent Dispatch Summary

- **Wave 1**: 2 — T1 → `quick`, T2 → `quick`
- **Wave 2**: 1 — T3 → `deep`
- **Wave 3**: 1 — T4 → `unspecified-high`
- **Wave 4**: 2 — T5 → `unspecified-high`, T6 → `unspecified-high`
- **Wave 5**: 1 — T7 → `writing`
- **FINAL**: 4 — F1 → `oracle`, F2 → `unspecified-high`, F3 → `unspecified-high`, F4 → `deep`

---

## TODOs

> Implementation + Test = UNE tâche. QA scenarios OBLIGATOIRES.
> **FORMAT** : labels en numéros nus `1.`, `2.` (PAS `T1.`/`Task 1.`/`Phase 1:`). Final Wave en `F1.`, `F2.`.

- [x] 1. session.go — ajouter `BlameEntry.Agent` + `BlameQuery.FilePaths`

  **What to do**:
  - Dans `internal/session/session.go`, struct `BlameEntry` (≈ lignes 985-994) : ajouter le champ `Agent string \`json:"agent"\`` (placer après `Provider`, comme dans `Summary`/`VoiceSummary` qui ont déjà `Agent`).
  - Dans `BlameQuery` (≈ lignes 996-1003) : ajouter `FilePaths []string` (commentaire : « optionnel — quand non vide, prime sur FilePath et matche plusieurs fichiers via IN(...) »). **Conserver** `FilePath string` pour rétro-compat.
  - RED d'abord : dans `internal/session/session_test.go` (ou nouveau `blame_test.go` si le test n'existe pas dans ce package), ajouter un test qui sérialise un `BlameEntry{Agent:"jarvis"}` en JSON et asserte la présence de `"agent":"jarvis"`.

  **Must NOT do**:
  - Ne PAS supprimer/renommer `FilePath` (rétro-compat).
  - Ne PAS toucher `FileChange` ni le schéma `file_changes`.
  - Ne PAS ajouter de logique métier dans ce package (types uniquement).

  **Recommended Agent Profile**:
  - **Category**: `quick` — Reason: ajout de 2 champs + 1 test de sérialisation, mécanique et localisé.
  - **Skills**: aucune
    - Évaluées et omises : `git-master` (commit trivial géré par la commit strategy), pas de domaine UI/sécurité.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (avec Task 2)
  - **Blocks**: Task 3
  - **Blocked By**: None (peut démarrer immédiatement)

  **References**:
  - `internal/session/session.go:985-994` — struct `BlameEntry` actuelle (sans Agent) ; suivre exactement ce style de tags JSON.
  - `internal/session/session.go:996-1003` — struct `BlameQuery` actuelle (FilePath unique) ; ajouter FilePaths à côté.
  - `internal/session/session.go:91` (`Summary.Agent`) et `:981` (`VoiceSummary.Agent`) — **pattern exact** du champ `Agent string \`json:"agent"\`` déjà utilisé ailleurs : copier ce tag.
  - **WHY**: le store fera `COALESCE(s.agent,'')` ; le champ doit donc tolérer la chaîne vide sans `omitempty` pour que la colonne soit toujours présente en JSON.

  **Acceptance Criteria**:
  - [ ] `go build ./internal/session/...` → OK
  - [ ] `go test ./internal/session/...` → PASS (nouveau test agent inclus)

  **QA Scenarios**:
  ```
  Scenario: BlameEntry sérialise le champ agent
    Tool: Bash (go test)
    Preconditions: champ Agent ajouté à BlameEntry, test écrit
    Steps:
      1. Lancer: go test ./internal/session/ -run Blame -v
      2. Assert: sortie contient "PASS" et le test agent est listé "--- PASS"
    Expected Result: exit code 0, test agent vert
    Failure Indicators: "FAIL", "undefined: BlameEntry.Agent", build error
    Evidence: .omo/evidence/task-1-agent-json.txt

  Scenario: BlameQuery.FilePaths compile et coexiste avec FilePath
    Tool: Bash (go build)
    Preconditions: FilePaths ajouté, FilePath conservé
    Steps:
      1. Lancer: go vet ./internal/session/
      2. Assert: pas d'erreur, exit code 0
    Expected Result: vet propre
    Failure Indicators: "FilePath redeclared", erreur de compilation
    Evidence: .omo/evidence/task-1-query-build.txt
  ```

  **Commit**: YES
  - Message: `feat(blame): add Agent field and FilePaths to blame types`
  - Files: `internal/session/session.go`, `internal/session/session_test.go`
  - Pre-commit: `go test ./internal/session/...`

- [x] 2. fileops.go — ajouter `ProjectFileEntry.LastAgent`

  **What to do**:
  - Dans `internal/session/fileops.go`, struct `ProjectFileEntry` (≈ lignes 464-476) : ajouter `LastAgent string \`json:"last_agent"\`` (à côté de `LastProvider`/`LastBranch`), pour porter l'agent de la dernière session ayant touché le fichier dans la vue projet.
  - RED : ajouter/étendre un test de sérialisation `ProjectFileEntry` qui asserte la présence de `"last_agent"`.

  **Must NOT do**:
  - Ne PAS modifier `TopFileEntry`.
  - Ne PAS ajouter de logique (types uniquement).

  **Recommended Agent Profile**:
  - **Category**: `quick` — Reason: ajout d'1 champ + assertion, trivial et isolé (fichier distinct de Task 1 → parallèle).
  - **Skills**: aucune.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (avec Task 1)
  - **Blocks**: Task 3
  - **Blocked By**: None

  **References**:
  - `internal/session/fileops.go:464-476` — struct `ProjectFileEntry` actuelle ; `LastProvider`/`LastBranch`/`LastCommitSHA` montrent le style des champs « Last* » à imiter.
  - `internal/storage/sqlite/sqlite.go:2175-2186` — CTE `last_session` qui produit déjà `last_branch/last_provider/last_commit_sha` ; Task 3 y ajoutera `last_agent`, donc le champ doit exister ici d'abord.
  - **WHY**: la vue `--project` (Task 5) affichera cet agent ; sans le champ, la donnée se perd entre store et CLI.

  **Acceptance Criteria**:
  - [ ] `go build ./internal/session/...` → OK
  - [ ] `go test ./internal/session/...` → PASS

  **QA Scenarios**:
  ```
  Scenario: ProjectFileEntry expose last_agent en JSON
    Tool: Bash (go test)
    Preconditions: champ LastAgent ajouté + test
    Steps:
      1. Lancer: go test ./internal/session/ -run ProjectFile -v
      2. Assert: sortie contient "PASS" et "last_agent"
    Expected Result: exit 0, test vert
    Failure Indicators: "FAIL", "undefined: LastAgent"
    Evidence: .omo/evidence/task-2-last-agent.txt

  Scenario: Pas de régression sur TopFileEntry
    Tool: Bash (go test)
    Preconditions: TopFileEntry inchangé
    Steps:
      1. Lancer: go test ./internal/session/ -run File -v
      2. Assert: tous les tests File* passent
    Expected Result: exit 0
    Failure Indicators: tout "FAIL"
    Evidence: .omo/evidence/task-2-no-regression.txt
  ```

  **Commit**: YES
  - Message: `feat(blame): add LastAgent to ProjectFileEntry`
  - Files: `internal/session/fileops.go`, `internal/session/fileops_test.go` (ou test existant)
  - Pre-commit: `go test ./internal/session/...`

- [x] 3. sqlite.go — `GetSessionsByFile` (agent + multi-fichiers) + `FilesForProject` (agent) + iface/mock

  **What to do**:
  - **`GetSessionsByFile`** (`internal/storage/sqlite/sqlite.go:1907-1932`) :
    - Ajouter `COALESCE(s.agent, '')` au SELECT (le JOIN `sessions s` existe déjà) et un champ correspondant dans le `rows.Scan(...)` → peupler `BlameEntry.Agent`.
    - Multi-fichiers : quand `query.FilePaths` est non vide, construire dynamiquement `WHERE fc.file_path IN (?, ?, ...)` avec les placeholders et args ; sinon conserver le `WHERE fc.file_path = ?` existant (rétro-compat via `query.FilePath`).
    - Conserver le support `ExcludeReads` déjà présent.
  - **`FilesForProject`** (`internal/storage/sqlite/sqlite.go:2148`, CTE `last_session` ≈ `:2175-2186`) : ajouter `last_agent` à la CTE (sélectionner `s.agent` de la dernière session par fichier, à côté de `last_branch`/`last_provider`/`last_commit_sha`) et mapper vers `ProjectFileEntry.LastAgent`.
  - **Interface store** (`internal/storage/store.go`) : si la signature de `GetSessionsByFile`/`FilesForProject` ne change pas (elles prennent déjà `BlameQuery`/un path), aucune modif d'interface. **Si** une signature change, mettre à jour `store.go` ET `internal/testutil/mock_store.go` en miroir.
  - RED : dans `internal/storage/sqlite/sqlite_test.go`, ajouter des tests table-driven :
    1. seed 2 sessions (`agent="jarvis"`, `agent="hermes"`) touchant `a.go` → `GetSessionsByFile{FilePath:"a.go"}` renvoie l'agent peuplé.
    2. seed `a.go`+`b.go` → `GetSessionsByFile{FilePaths:["a.go","b.go"]}` renvoie les entrées des 2 fichiers.
    3. `FilesForProject(path)` → la dernière session par fichier expose `LastAgent`.
    4. agent vide en DB → `Agent == ""` (pas de ligne perdue).

  **Must NOT do**:
  - Ne PAS `ALTER` la table `file_changes` ni ajouter de colonne (réutiliser le JOIN `sessions`).
  - Ne PAS faire de N+1 : l'agent vient du même SELECT JOIN, jamais d'une requête par entrée.
  - Ne PAS supporter glob/wildcard dans les paths (liste explicite uniquement).
  - Ne PAS casser `internal/web/handlers.go` (seul consommateur actuel de `FilesForProject`).

  **Recommended Agent Profile**:
  - **Category**: `deep` — Reason: SQL dynamique (IN variadique), CTE multi-colonnes, cohérence interface/mock, suite de tests store — raisonnement et rigueur requis.
  - **Skills**: aucune (Go pur + SQLite).
    - Évaluées et omises : `git-master` (commit géré ailleurs).

  **Parallelization**:
  - **Can Run In Parallel**: NO (sqlite.go sérialisé)
  - **Parallel Group**: Wave 2 (seul)
  - **Blocks**: Task 4
  - **Blocked By**: Task 1, Task 2

  **References**:
  - `internal/storage/sqlite/sqlite.go:1907-1932` — `GetSessionsByFile` actuel : JOIN `sessions s ⋈ file_changes fc`, `WHERE fc.file_path = ?`, `ExcludeReads`. Ajouter `s.agent` au SELECT/Scan et la branche `IN (...)`.
  - `internal/storage/sqlite/sqlite.go:46-51` — schéma `file_changes` (id/session_id/file_path/change_type) : confirme l'absence d'`agent` côté table → l'agent DOIT venir du JOIN `sessions`.
  - `internal/storage/sqlite/sqlite.go:2148` + `:2175-2186` — `FilesForProject` et sa CTE `last_session` produisant `last_branch/last_provider/last_commit_sha` : ajouter `last_agent` au même endroit.
  - `internal/session/session.go:BlameEntry` (Task 1) — cible du Scan (`.Agent`).
  - `internal/session/fileops.go:ProjectFileEntry` (Task 2) — cible du mapping (`.LastAgent`).
  - `internal/storage/store.go` + `internal/testutil/mock_store.go` — interface + mock à garder synchrones si signature change.
  - `internal/web/handlers.go` — consommateur de `FilesForProject` ; vérifier qu'il compile toujours (ajout de champ = non-breaking).
  - **WHY**: c'est le cœur de la feature — toutes les couches au-dessus (service/CLI/MCP) ne font que relayer ces deux requêtes.

  **Acceptance Criteria**:
  - [ ] `go build ./...` → OK (web handlers inclus)
  - [ ] `go test ./internal/storage/... -run 'SessionsByFile|FilesForProject' -v` → PASS (≥4 nouveaux cas)
  - [ ] `go test ./internal/...` → PASS (pas de régression mock/interface)

  **QA Scenarios**:
  ```
  Scenario: GetSessionsByFile remonte l'agent depuis le JOIN sessions
    Tool: Bash (go test)
    Preconditions: DB temp seedée, 2 sessions avec agents distincts touchant a.go
    Steps:
      1. Lancer: go test ./internal/storage/sqlite/ -run SessionsByFile -v
      2. Assert: sortie "PASS" ; assertions sur Agent=="jarvis"/"hermes" vertes
    Expected Result: exit 0, agents corrects
    Failure Indicators: "FAIL", Agent vide alors que la session a un agent, "no such column"
    Evidence: .omo/evidence/task-3-agent-join.txt

  Scenario: Requête multi-fichiers via FilePaths IN(...)
    Tool: Bash (go test)
    Preconditions: a.go + b.go seedés par des sessions différentes
    Steps:
      1. Lancer: go test ./internal/storage/sqlite/ -run SessionsByFile_Multi -v
      2. Assert: le résultat contient des entrées des DEUX fichiers
    Expected Result: exit 0, couverture des 2 fichiers
    Failure Indicators: une seule clé matchée, panic placeholders SQL
    Evidence: .omo/evidence/task-3-multifile.txt

  Scenario: FilesForProject expose last_agent et n'inclut pas N+1
    Tool: Bash (go test)
    Preconditions: projet seedé, ≥2 fichiers, dernière session par fichier identifiable
    Steps:
      1. Lancer: go test ./internal/storage/sqlite/ -run FilesForProject -v
      2. Assert: LastAgent peuplé pour la dernière session de chaque fichier
    Expected Result: exit 0
    Failure Indicators: LastAgent vide, mauvaise "dernière" session
    Evidence: .omo/evidence/task-3-project-agent.txt

  Scenario: Non-régression web handlers (consommateur FilesForProject)
    Tool: Bash (go build)
    Preconditions: champ LastAgent ajouté (additif)
    Steps:
      1. Lancer: go build ./internal/web/...
      2. Assert: exit 0
    Expected Result: build propre
    Failure Indicators: erreur de compilation handlers
    Evidence: .omo/evidence/task-3-web-build.txt
  ```

  **Commit**: YES
  - Message: `feat(blame): surface agent + multi-file/project queries in store`
  - Files: `internal/storage/sqlite/sqlite.go`, `internal/storage/sqlite/sqlite_test.go`, `internal/storage/store.go` (si signature), `internal/testutil/mock_store.go` (si signature)
  - Pre-commit: `go test ./internal/storage/... ./internal/...`

- [x] 4. session_blame.go — `BlameRequest.FilePaths` + logique multi-fichiers + mode projet

  **What to do**:
  - Dans `internal/service/session_blame.go` (`BlameRequest`/`BlameResult`/`Blame` ≈ `:13-31`) :
    - Ajouter `FilePaths []string` à `BlameRequest` (garder `FilePath string`).
    - Ajouter un indicateur de mode projet : `ProjectPath string` (quand non vide, route vers `FilesForProject` du store au lieu de `GetSessionsByFile`).
    - Dans `Blame(...)` : construire la `BlameQuery` en passant `FilePath` OU `FilePaths` selon ce qui est fourni ; quand `ProjectPath` est fourni, appeler le chemin projet et renvoyer un `BlameResult` (ou structure existante) portant fichier → dernière session → agent.
    - Normaliser les paths (mêmes règles que l'existant — chemin absolu/relatif au repo) avant de requêter.
  - RED : dans `internal/service/session_blame_test.go` (ou via un mock store) :
    1. `FilePaths` à 2 entrées → le service appelle le store avec `FilePaths` et agrège.
    2. `ProjectPath` fourni → le service appelle `FilesForProject` et renvoie les `LastAgent`.
    3. rétro-compat : `FilePath` seul → comportement identique à aujourd'hui.

  **Must NOT do**:
  - Ne PAS dupliquer la logique SQL (déléguer au store).
  - Ne PAS introduire de nouvelle commande/route ici (service uniquement).
  - Ne PAS rendre `FilePath` et `ProjectPath` mutuellement obligatoires d'une façon qui casse l'appel mono-fichier existant.

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high` — Reason: orchestration service + branchements de mode + tests sur mock, effort modéré mais transverse.
  - **Skills**: aucune.

  **Parallelization**:
  - **Can Run In Parallel**: NO
  - **Parallel Group**: Wave 3 (seul)
  - **Blocks**: Task 5, Task 6
  - **Blocked By**: Task 3

  **References**:
  - `internal/service/session_blame.go:13-27` — `BlameRequest`/`BlameResult` actuels ; ajouter `FilePaths`/`ProjectPath` à `BlameRequest`.
  - `internal/service/session_blame.go:31` — méthode `Blame(...)` : point d'insertion du branchement mono/multi/projet.
  - `internal/session/session.go:BlameQuery` (Task 1) — structure passée au store (`FilePath`/`FilePaths`).
  - `internal/storage/sqlite/sqlite.go:GetSessionsByFile` / `FilesForProject` (Task 3) — méthodes store appelées.
  - `internal/testutil/mock_store.go` — mock à utiliser pour les tests service.
  - **WHY**: le service est le point unique où CLI (Task 5) et MCP (Task 6) convergent ; il doit exposer multi-fichiers ET mode projet pour que les deux adaptateurs restent fins.

  **Acceptance Criteria**:
  - [ ] `go build ./internal/service/...` → OK
  - [ ] `go test ./internal/service/ -run Blame -v` → PASS (≥3 cas : multi, projet, rétro-compat)

  **QA Scenarios**:
  ```
  Scenario: Blame multi-fichiers délègue FilePaths au store
    Tool: Bash (go test)
    Preconditions: mock store enregistrant la BlameQuery reçue
    Steps:
      1. Lancer: go test ./internal/service/ -run Blame_MultiFile -v
      2. Assert: le mock a reçu FilePaths à 2 entrées ; résultat agrégé
    Expected Result: exit 0
    Failure Indicators: store appelé avec FilePath seul, agrégation manquante
    Evidence: .omo/evidence/task-4-service-multifile.txt

  Scenario: Mode projet route vers FilesForProject
    Tool: Bash (go test)
    Preconditions: mock retournant des ProjectFileEntry avec LastAgent
    Steps:
      1. Lancer: go test ./internal/service/ -run Blame_Project -v
      2. Assert: FilesForProject appelé ; LastAgent propagé dans le résultat
    Expected Result: exit 0
    Failure Indicators: GetSessionsByFile appelé à tort, LastAgent perdu
    Evidence: .omo/evidence/task-4-service-project.txt

  Scenario: Rétro-compat mono-fichier
    Tool: Bash (go test)
    Preconditions: appel avec FilePath seul
    Steps:
      1. Lancer: go test ./internal/service/ -run Blame_SingleFile -v
      2. Assert: comportement identique à l'existant
    Expected Result: exit 0
    Failure Indicators: changement de comportement mono-fichier
    Evidence: .omo/evidence/task-4-service-single.txt
  ```

  **Commit**: YES
  - Message: `feat(blame): multi-file and project blame in service layer`
  - Files: `internal/service/session_blame.go`, `internal/service/session_blame_test.go`
  - Pre-commit: `go test ./internal/service/...`

- [x] 5. blamecmd.go — multi-args variadiques + flag `--project` + colonne `AGENT`

  **What to do**:
  - Dans `pkg/cmd/blamecmd/blamecmd.go` :
    - Passer `cobra.ExactArgs(1)` à `cobra.MinimumNArgs(1)` (sauf si `--project` fourni, où 0 arg est valide) ; collecter tous les `args` comme liste de fichiers → `BlameRequest.FilePaths`.
    - Ajouter un flag `--project <path>` (string) dans `Options` → quand fourni, peupler `BlameRequest.ProjectPath` et router vers le mode projet du service.
    - **Sortie table** : ajouter une colonne `AGENT` ; afficher `-` quand l'agent est vide (`""`). Ne PAS ajouter la colonne en mode `--quiet` (qui reste IDs bruts uniquement).
    - **Sortie `--json`** : inclure `agent` (déjà porté par `BlameEntry`, donc automatique si on sérialise la structure).
    - Mode `--project` : table fichier → dernière session → agent (mapper `ProjectFileEntry`).
  - RED : dans `pkg/cmd/blamecmd/blamecmd_test.go`, tests :
    1. 2 args → `BlameRequest.FilePaths` à 2 entrées.
    2. `--project p` → `BlameRequest.ProjectPath == p`.
    3. rendu table contient l'en-tête `AGENT` ; agent vide rendu `-`.

  **Must NOT do**:
  - Ne PAS ajouter la colonne AGENT en `--quiet` (casse les consommateurs qui parsent les IDs).
  - Ne PAS supporter glob/wildcard sur les args.
  - Ne PAS créer de nouvelle sous-commande (réutiliser `blame` + `--project`).
  - Ne PAS modifier d'autres commandes (`list`, etc.).

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high` — Reason: parsing Cobra (args variadiques + flag), formatage table conditionnel, tests CLI — transverse mais sans logique métier lourde.
  - **Skills**: aucune.

  **Parallelization**:
  - **Can Run In Parallel**: YES (fichier distinct de Task 6)
  - **Parallel Group**: Wave 4 (avec Task 6)
  - **Blocks**: Task 7
  - **Blocked By**: Task 4

  **References**:
  - `pkg/cmd/blamecmd/blamecmd.go` — `Options`, `cobra.ExactArgs(1)`, branches de sortie table/`--quiet`/`--json` actuelles ; point d'insertion du flag `--project` et de la colonne AGENT.
  - `internal/service/session_blame.go:BlameRequest` (Task 4) — champs `FilePaths`/`ProjectPath` à peupler.
  - `internal/session/session.go:BlameEntry.Agent` (Task 1) — source de la colonne AGENT / champ JSON.
  - `internal/session/fileops.go:ProjectFileEntry.LastAgent` (Task 2) — source de l'agent en mode projet.
  - **WHY**: c'est la surface utilisateur principale ; le rendu `-` pour agent vide et la non-pollution de `--quiet` sont les garde-fous de rétro-compat agent-friendly.

  **Acceptance Criteria**:
  - [ ] `go build ./pkg/cmd/blamecmd/...` → OK
  - [ ] `go test ./pkg/cmd/blamecmd/ -v` → PASS
  - [ ] `make build` → `bin/aisync` produit

  **QA Scenarios**:
  ```
  Scenario: blame multi-fichiers sur binaire réel agrège 2 fichiers
    Tool: interactive_bash (tmux)
    Preconditions: make build OK ; DB temp seedée (2 sessions, agents distincts, a.go+b.go) ; AISYNC_DB pointant dessus
    Steps:
      1. tmux: bin/aisync blame a.go b.go
      2. Assert: stdout liste des entrées provenant de a.go ET b.go, colonne AGENT visible
      3. echo exit: $? == 0
    Expected Result: 2 fichiers couverts, agents affichés
    Failure Indicators: "accepts 1 arg(s)", un seul fichier, panic
    Evidence: .omo/evidence/task-5-cli-multifile.txt

  Scenario: --json contient le champ agent
    Tool: interactive_bash (tmux)
    Preconditions: idem
    Steps:
      1. tmux: bin/aisync blame --json a.go
      2. Assert: la sortie JSON contient "agent"
    Expected Result: champ agent présent
    Failure Indicators: pas de clé agent
    Evidence: .omo/evidence/task-5-cli-json.txt

  Scenario: --project liste fichier → dernière session → agent
    Tool: interactive_bash (tmux)
    Preconditions: projet seedé
    Steps:
      1. tmux: bin/aisync blame --project <path>
      2. Assert: table avec colonnes fichier / session / AGENT
    Expected Result: vue projet rendue
    Failure Indicators: erreur flag inconnu, table vide alors que données seedées
    Evidence: .omo/evidence/task-5-cli-project.txt

  Scenario: agent vide rendu "-" et --quiet inchangé
    Tool: interactive_bash (tmux)
    Preconditions: 1 session sans agent
    Steps:
      1. tmux: bin/aisync blame a.go   → assert ligne agent == "-"
      2. tmux: bin/aisync blame --quiet a.go  → assert sortie = IDs bruts, sans colonne AGENT
    Expected Result: table "-" ; quiet pur
    Failure Indicators: "" affiché brut, colonne AGENT polluant --quiet
    Evidence: .omo/evidence/task-5-cli-empty-quiet.txt
  ```

  **Commit**: YES
  - Message: `feat(blame): CLI multi-file args, --project, AGENT column`
  - Files: `pkg/cmd/blamecmd/blamecmd.go`, `pkg/cmd/blamecmd/blamecmd_test.go`
  - Pre-commit: `go test ./pkg/cmd/blamecmd/...`

- [x] 6. tools.go + server.go — MCP `aisync_blame` accepte `files[]` + `file`, renvoie l'agent

  **What to do**:
  - Dans `internal/mcp/server.go` (≈ `:171-177`, déclaration du tool `aisync_blame`) : ajouter un paramètre `files` de type array de strings (optionnel) à côté du `file` (string) existant. Documenter dans la description que `files` prime sur `file` quand fourni.
  - Dans `internal/mcp/tools.go` (`handleBlame` ≈ `:339-374`) : lire `files` (array) si présent → `BlameRequest.FilePaths` ; sinon retomber sur `file` → `BlameRequest.FilePath`. Le `BlameResult` renvoyé portera automatiquement `agent` une fois `BlameEntry.Agent` peuplé (Task 1+3).
  - RED : dans `internal/mcp/server_test.go` (≈ `:393-445`, suite blame existante) :
    1. appel avec `file` (string) → comportement actuel + champ agent présent dans le résultat.
    2. appel avec `files: ["a.go","b.go"]` → entrées des 2 fichiers.
    3. ni `file` ni `files` → erreur de validation claire.

  **Must NOT do**:
  - Ne PAS retirer le paramètre `file` (rétro-compat MCP stricte).
  - Ne PAS diverger de la sémantique CLI (mêmes règles multi-fichiers).
  - Ne PAS exposer le mode `--project` via MCP dans CE plan (hors scope MCP ici ; uniquement `file`/`files`).

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high` — Reason: schéma d'outil MCP (mark3labs/mcp-go), parsing d'args optionnels, tests JSON-RPC — transverse, fichier distinct de Task 5 → parallèle.
  - **Skills**: aucune.

  **Parallelization**:
  - **Can Run In Parallel**: YES (fichiers distincts de Task 5)
  - **Parallel Group**: Wave 4 (avec Task 5)
  - **Blocks**: Task 7
  - **Blocked By**: Task 4

  **References**:
  - `internal/mcp/server.go:171-177` — enregistrement du tool `aisync_blame` ; ajouter le param array `files` au schéma.
  - `internal/mcp/tools.go:339-374` — `handleBlame` : parsing des args + appel service ; point d'insertion de la branche `files`/`file`.
  - `internal/mcp/server_test.go:393-445` — suite de tests blame MCP existante ; modèle pour les nouveaux cas.
  - `internal/service/session_blame.go:BlameRequest` (Task 4) — `FilePaths`/`FilePath` à peupler.
  - **WHY**: garder MCP et CLI synchrones permet à un agent d'utiliser indifféremment l'outil MCP ou la commande ; `files` est la clé pour interroger plusieurs fichiers en un appel.

  **Acceptance Criteria**:
  - [ ] `go build ./internal/mcp/...` → OK
  - [ ] `go test ./internal/mcp/ -run Blame -v` → PASS (≥3 cas)

  **QA Scenarios**:
  ```
  Scenario: aisync_blame MCP avec file (string) renvoie agent
    Tool: Bash (go test)
    Preconditions: store de test seedé, agent peuplé
    Steps:
      1. Lancer: go test ./internal/mcp/ -run Blame_SingleFile -v
      2. Assert: résultat JSON contient agent ; comportement string inchangé
    Expected Result: exit 0
    Failure Indicators: "FAIL", agent absent du résultat
    Evidence: .omo/evidence/task-6-mcp-file.txt

  Scenario: aisync_blame MCP avec files[] couvre plusieurs fichiers
    Tool: Bash (go test)
    Preconditions: a.go + b.go seedés
    Steps:
      1. Lancer: go test ./internal/mcp/ -run Blame_Files -v
      2. Assert: résultat couvre les 2 fichiers
    Expected Result: exit 0
    Failure Indicators: un seul fichier, erreur de parsing array
    Evidence: .omo/evidence/task-6-mcp-files.txt

  Scenario: ni file ni files → erreur de validation
    Tool: Bash (go test)
    Preconditions: aucun argument fichier
    Steps:
      1. Lancer: go test ./internal/mcp/ -run Blame_NoArg -v
      2. Assert: erreur claire renvoyée (pas de panic)
    Expected Result: exit 0 (le test attend l'erreur)
    Failure Indicators: panic, succès silencieux
    Evidence: .omo/evidence/task-6-mcp-noarg.txt
  ```

  **Commit**: YES
  - Message: `feat(blame): MCP aisync_blame accepts files[] and returns agent`
  - Files: `internal/mcp/tools.go`, `internal/mcp/server.go`, `internal/mcp/server_test.go`
  - Pre-commit: `go test ./internal/mcp/...`

- [x] 7. docs — `architecture/blame.md` + README : agent / multi-fichiers / `--project`

  **What to do**:
  - Mettre à jour (ou créer si absent) `architecture/blame.md` : décrire l'attribution par agent (JOIN `sessions`, `COALESCE`), la requête multi-fichiers (`WHERE file_path IN (...)`), et le mode projet (`FilesForProject` + `LastAgent`). Inclure un schéma de flux CLI → service → store.
  - Mettre à jour `README.md` : dans la ligne `aisync blame` du tableau des commandes et/ou la section MCP, mentionner : colonne AGENT, `aisync blame <f1> <f2> ...`, `aisync blame --project <path>`, et l'argument MCP `files`.
  - Vérifier la cohérence des exemples avec la sortie réelle du binaire (les copier depuis les evidence de Task 5).

  **Must NOT do**:
  - Ne PAS documenter de fonctionnalité hors scope (ping/notify, `--agent` sur list, skill).
  - Ne PAS inventer de flags non implémentés.

  **Recommended Agent Profile**:
  - **Category**: `writing` — Reason: rédaction de documentation technique, aucun code.
  - **Skills**: aucune.

  **Parallelization**:
  - **Can Run In Parallel**: NO (dépend du comportement final figé par T5/T6)
  - **Parallel Group**: Wave 5 (seul)
  - **Blocks**: F1-F4
  - **Blocked By**: Task 5, Task 6

  **References**:
  - `README.md` — tableau des commandes (ligne `aisync blame`) + tableau MCP (`aisync_blame`) ; sources à amender.
  - `architecture/` — répertoire des docs d'archi (modèle de format à suivre) ; y placer/MAJ `blame.md`.
  - `.omo/evidence/task-5-*` / `task-6-*` — sorties réelles à recopier comme exemples vérifiés.
  - **WHY**: la doc est la dernière garantie que la feature est découvrable par un humain ET par un agent lisant le README.

  **Acceptance Criteria**:
  - [ ] `architecture/blame.md` mentionne agent + multi-fichiers + `--project`
  - [ ] `README.md` décrit la colonne AGENT, les multi-args, `--project`, et l'argument MCP `files`
  - [ ] Aucun flag inexistant documenté (vérif croisée avec `blamecmd.go`)

  **QA Scenarios**:
  ```
  Scenario: La doc référence les capacités réelles
    Tool: Bash (grep)
    Preconditions: docs mises à jour
    Steps:
      1. Lancer: grep -i "project" architecture/blame.md && grep -i "agent" README.md
      2. Assert: les deux grep matchent
    Expected Result: exit 0, motifs trouvés
    Failure Indicators: grep vide
    Evidence: .omo/evidence/task-7-docs-grep.txt

  Scenario: Pas de flag fantôme documenté
    Tool: Bash (grep)
    Preconditions: docs + blamecmd.go
    Steps:
      1. Lancer: grep -oE "\-\-[a-z-]+" README.md (section blame) puis vérifier chaque flag existe dans blamecmd.go
      2. Assert: tout flag documenté existe dans le code
    Expected Result: 100% des flags doc présents dans le code
    Failure Indicators: flag documenté absent du code
    Evidence: .omo/evidence/task-7-no-ghost-flag.txt
  ```

  **Commit**: YES
  - Message: `docs(blame): document agent attribution, multi-file, project mode`
  - Files: `architecture/blame.md`, `README.md`
  - Pre-commit: aucun (docs) — relecture grep

---

## Final Verification Wave (MANDATORY — after ALL implementation tasks)

> 4 agents de revue en PARALLÈLE. TOUS doivent APPROUVER. Présenter les résultats consolidés et obtenir un « okay » explicite avant de clore.
> **Ne jamais cocher F1-F4 avant l'okay utilisateur.** Rejet → corriger → relancer → re-présenter → attendre okay.

- [x] F1. **Plan Compliance Audit** — `oracle`
  Lire le plan de bout en bout. Pour chaque « Must Have » : vérifier que l'implémentation existe (lire fichier, lancer `aisync blame`). Pour chaque « Must NOT Have » : grep le codebase pour les patterns interdits (ping/notify, `--agent` sur list, ALTER de `file_changes`, glob, N+1) — rejeter avec file:line si trouvé. Vérifier les evidence dans `.omo/evidence/`.
  Output: `Must Have [N/N] | Must NOT Have [N/N] | Tasks [N/N] | VERDICT: APPROVE/REJECT`

- [x] F2. **Code Quality Review** — `unspecified-high`
  Lancer `make lint` + `go vet ./...` + `make test`. Revoir les fichiers modifiés : `as any` Go n'existe pas mais traquer les `interface{}` injustifiés, catches vides (`_ = err` non motivés), prints de debug, code commenté, imports inutilisés. Slop IA : commentaires excessifs, sur-abstraction, noms génériques (data/result/tmp).
  Output: `Build [PASS/FAIL] | Lint [PASS/FAIL] | Tests [N pass/N fail] | Files [N clean/N issues] | VERDICT`

- [x] F3. **Real Manual QA** — `unspecified-high`
  Partir d'un état propre (`make build`, DB temp seedée avec ≥2 agents distincts + fichiers partagés). Exécuter CHAQUE scénario QA de CHAQUE tâche, capturer les evidence. Tester l'intégration : `blame` mono+multi+`--project`+`--json`+`--quiet` ensemble ; MCP `aisync_blame` avec `file` puis `files`. Edge cases : fichier inexistant, agent vide, projet sans sessions. Evidence dans `.omo/evidence/final-qa/`.
  Output: `Scenarios [N/N pass] | Integration [N/N] | Edge Cases [N tested] | VERDICT`

- [x] F4. **Scope Fidelity Check** — `deep`
  Pour chaque tâche : lire « What to do », lire le diff réel (`git log`/`git diff`). Vérifier 1:1 — tout le spec construit (rien manquant), rien au-delà (pas de creep, notamment pas de `--agent`/`--status`/glob/skill/ALTER `file_changes`). Vérifier « Must NOT do ». Détecter contamination inter-tâches (Task N touchant les fichiers de Task M). Signaler tout changement non justifié.
  Output: `Tasks [N/N compliant] | Contamination [CLEAN/N issues] | Unaccounted [CLEAN/N files] | VERDICT`

---

## Commit Strategy

- **1**: `feat(blame): add Agent field and FilePaths to blame types` — session.go, `go test ./internal/session/...`
- **2**: `feat(blame): add LastAgent to ProjectFileEntry` — fileops.go, `go test ./internal/session/...`
- **3**: `feat(blame): surface agent + multi-file/project queries in store` — sqlite.go, store.go, mock_store.go, `go test ./internal/storage/...`
- **4**: `feat(blame): multi-file and project blame in service layer` — session_blame.go, `go test ./internal/service/...`
- **5**: `feat(blame): CLI multi-file args, --project, AGENT column` — blamecmd.go, `go test ./pkg/cmd/blamecmd/...`
- **6**: `feat(blame): MCP aisync_blame accepts files[] and returns agent` — tools.go, server.go, `go test ./internal/mcp/...`
- **7**: `docs(blame): document agent attribution, multi-file, project mode` — architecture/blame.md, README.md

---

## Success Criteria

### Verification Commands
```bash
make test                                  # Expected: ok, 0 failures
make lint                                  # Expected: 0 issues
aisync blame --json internal/session/session.go | grep '"agent"'   # Expected: agent field present
aisync blame internal/a.go internal/b.go   # Expected: entries from both files
aisync blame --project "$(pwd)"            # Expected: file → last session → agent table
```

### Final Checklist
- [ ] Tous les « Must Have » présents
- [ ] Tous les « Must NOT Have » absents
- [ ] Tous les tests passent
- [ ] F1-F4 APPROVE + okay utilisateur obtenu
