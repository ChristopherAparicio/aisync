# AI5 — Normalisation des chemins `file_changes` (absolu → relatif-au-projet)

## TL;DR

> **Quick Summary**: Rendre `aisync blame` fiable partout (CWD, équipe, remote AI5, worktrees) en stockant les chemins **relatifs à la racine projet** pour les fichiers intra-projet, tout en gardant **absolus** les fichiers hors-projet (~13%). Découpage 2 phases : (P1) la query blame matche les 2 formes — débloque immédiatement sans rien casser ; (P2) normalisation à l'écriture + backfill manuel des 36k lignes existantes.
>
> **Deliverables**:
> - `internal/session/filepath.go` : `NormalizeFilePath(filePath, projectRoot)` pure, idempotente, cross-OS
> - P1 : query blame résout l'input et matche **absolu legacy + relatif** (CLI + store)
> - P2 : normalisation au **choke point d'écriture** (`Store.Save` + `ReplaceSessionFiles`) → couvre capture / import / pull
> - P2 : `aisync backfill normalize-paths` (manuel, `--dry-run`, `--json`) pour les 36k lignes
> - Couverture TDD complète + requêtes de vérification post-backfill + QA binaire réel
>
> **Estimated Effort**: Medium (1–2 j)
> **Parallel Execution**: Partiel — P1 indépendant de P2 ; socle (Task 1) bloque tout
> **Critical Path**: Task 1 → Task 2 (P1) ‖ (Task 3 → Task 4 → Task 5) (P2) → F1

---

## Context

### Original Request
Projet multi-agent en désordre (Confide, ~187 fichiers non commités, ~8 streams d'agents concurrents). Besoin : `aisync blame <fichier>` doit marcher depuis un `git status` (chemins relatifs) et à travers machines / équipe / remote AI5. Aujourd'hui cassé : la DB stocke des chemins **absolus**, donc `aisync blame internal/mcp/tools.go` → « no sessions » alors que le chemin absolu complet → 42 sessions.

### Données réelles (DB prod `~/.aisync/sessions.db`, 36 764 lignes `file_changes`)
- 32 986 absolus, 3 778 relatifs/autres.
- Sur les absolus, **87 % (28 804)** sont préfixés par le `project_path` de leur session → intra-projet, normalisables.
- **13 % (~4 182)** hors racine projet (ex. `/tmp/cycloplan-ios.png`) → resteront absolus.
- `project_path` varie : repos normaux + worktrees OpenCode (`.../worktree/<hash>/...`).

### Findings (code réel, vérifié)
- **Choke point unique** : capture, import et pull convergent tous vers `Store.Save()` — `session.ProjectPath` y est déjà en scope. Insert file_changes : `internal/storage/sqlite/sqlite.go:261-263`.
- **2e site d'écriture** : `ReplaceSessionFiles()` `sqlite.go:1976-2000` (extracteur blame `BackfillFileBlame`) — reçoit `records []SessionFileRecord` + sessionID, **sans** project_path.
- **Gap pull/import confirmé** : aucune normalisation aujourd'hui. Import → `internal/service/session_export.go:121-166` (`Unmarshal` → `store.Save`). Pull → `internal/gitsync/service.go:202-215` (`Unmarshal` → `store.Save`). Push/export sérialisent la session telle quelle (`gitsync/service.go:100-119`).
- **Format sync** : `FileChange{FilePath string `json:"file_path"`, ChangeType}` — `internal/session/session.go:597-600`. Pas de `tool_name` sur la branche sync (colonne locale seulement).
- **Helper existant** : `cleanFilePath` (`internal/session/fileops.go:335-347`) fait déjà `filepath.ToSlash` + trim trailing slash. `NormalizeFilePath` = sibling même package.
- **Provenance project_path** : git root via `gitClient.TopLevel()` (`pkg/cmd/capture/capture.go`), fillé dans `internal/capture/service.go:196-201` si l'exporter ne l'a pas posé. Overrides providers : Claude `line.Cwd`, OpenCode `sess.Directory`, Cursor vide.
- **Migrations** : pas de table de version. `runMigrations()` (`sqlite.go:3102+`) tourne à chaque `sqlite.New()`. → un auto-migrate re-scannerait les 13% à chaque boot. **Décision : backfill manuel.**
- `aisync backfill files` existe déjà (`pkg/cmd/backfillcmd/files.go`) mais fait de l'**extraction**, pas de la normalisation → on ajoute une sous-commande sœur.

### Oracle Review (stratégie migration)
- Flag majeur : migrer la DB locale ne suffit pas — il faut normaliser aussi à **import/pull** sinon un teammate ré-importe des absolus. (Adressé par la normalisation au choke point `Store.Save`, qui couvre le pull.)
- Fonction pure idempotente, comparaison case-insensitive (macOS/Win), sortie casing d'origine, séparateurs `/`, `projectRoot==""` → passthrough, symlinks **non résolus**, hors-projet → absolu.
- Vérif post-migration : anti double-stripping, distribution des counts, anti-null, spot-check.

### Décisions d'archi (tranchées)
| Décision | Choix | Raison |
|---|---|---|
| Lieu de normalisation à l'écriture | `Store.Save()` + `ReplaceSessionFiles()` (storage layer) | 1 funnel garanti ; ProjectPath déjà en scope ; impossible d'oublier capture/import/pull |
| Migration historique | commande manuelle `aisync backfill normalize-paths` | données partagées = action délibérée + vérifiable ; évite re-scan permanent des 13% au boot |
| Hors-projet (13%) | rester absolu, discriminé par « sous project_path » (jamais par slash, à cause de Windows `C:\`) | seul moyen de les retrouver localement ; pas portable par nature de toute façon |

---

## Work Objectives

### Core Objective
`file_changes.file_path` devient **relatif-au-projet** pour l'intra-projet (portable équipe/remote), **absolu** pour le hors-projet ; `aisync blame` matche les deux formes pendant et après la transition.

### Concrete Deliverables
- `internal/session/filepath.go` + tests : `NormalizeFilePath`
- `pkg/cmd/blamecmd/blamecmd.go` + `internal/storage/sqlite/sqlite.go` : query dual-format (P1)
- `internal/storage/sqlite/sqlite.go` : normalisation dans `Store.Save` + `ReplaceSessionFiles` (P2)
- `pkg/cmd/backfillcmd/normalize_paths.go` + service `NormalizePaths` (P2)
- `architecture/blame.md` : corriger L183 (fausse affirmation « CLI normalizes the input path ») + documenter le modèle de stockage

---

## Tasks

### Task 1 — Socle : `NormalizeFilePath` pure (TDD)  [BLOQUE TOUT]
**WHERE** `internal/session/filepath.go` (nouveau) + `internal/session/filepath_test.go`
**WHAT** Fonction pure `NormalizeFilePath(filePath, projectRoot string) string` :
- `projectRoot==""` → retourne `filePath` inchangé
- normalise séparateurs en `/` (in & out)
- si `filePath` déjà relatif (`!path.IsAbs`) → retourne inchangé (**idempotence**)
- trim trailing `/\` sur projectRoot
- comparaison **case-insensitive** ; si `==` root → `"."` ; si sous `root+"/"` → strip avec **casing d'origine** ; sinon → retourne l'absolu normalisé-slash
- **ne résout pas** les symlinks
**VERIFY** Tests RED→GREEN couvrant : intra-projet, hors-projet, déjà-relatif (idempotent rejoué 2×), trailing slash, `projectRoot==""`, `file==root` (`"."`), casse macOS, séparateurs Windows (`C:\a\b` sous `C:\a`), worktree path. `go test ./internal/session/ -run Normalize`.

### Task 2 — P1 : query blame dual-format (TDD)  [dépend de Task 1]
**WHERE** `pkg/cmd/blamecmd/blamecmd.go` (résolution input), `internal/storage/sqlite/sqlite.go` (`GetSessionsByFile`, `FilesForProject`)
**WHAT** Pour chaque arg : résoudre en absolu (CWD/git-root via `gitClient.TopLevel()`), calculer aussi la forme relative-au-git-root ; passer **les deux candidats** ; SQL matche `WHERE file_path IN (abs, rel)`. Corrige le bug actuel (`opts.FilePaths = args` verbatim) sans changer le stockage.
**VERIFY** Test : DB seedée avec une ligne **absolue** + une **relative** ; `blame <relatif>` et `blame <absolu>` retournent la session dans les deux cas. QA binaire réel : `aisync blame internal/mcp/tools.go` depuis le repo → trouve les sessions (aujourd'hui « no sessions »).
**OUTCOME** Workflow Confide débloqué **immédiatement**, zéro régression, zéro changement de données.

### Task 3 — P2 : normalisation au choke point d'écriture (TDD)  [dépend de Task 1]
**WHERE** `internal/storage/sqlite/sqlite.go` — `Store.Save` (L261-263) + `ReplaceSessionFiles` (L1976-2000, ajouter param `projectRoot string`, MAJ caller dans le service blame-extractor)
**WHAT** Avant insert, `fc.FilePath = session.NormalizeFilePath(fc.FilePath, <projectRoot>)`. Save utilise `session.ProjectPath` ; ReplaceSessionFiles reçoit le projectRoot du caller (qui a la session).
**VERIFY** Test Save : session avec project_path + path absolu intra-projet → ligne stockée relative ; path hors-projet → reste absolu. Test pull (`gitsync`) : importer une session JSON à paths absolus → DB stocke relatif (couvre le gap Oracle). `go test ./internal/storage/... ./internal/gitsync/...`.

### Task 4 — P2 : commande `aisync backfill normalize-paths` (TDD)  [dépend de Task 1, 3]
**WHERE** `pkg/cmd/backfillcmd/normalize_paths.go` (nouveau, enregistrer dans `backfillcmd.go:NewCmdBackfill`) + service `NormalizePaths(ctx)` dans `internal/service/`
**WHAT** Batch sur `file_changes ⋈ sessions` (WHERE file_path absolu : `LIKE '/%' OR LIKE '_:%'`), applique `NormalizeFilePath`, `UPDATE` seulement si résultat ≠ original, tx par 1000. Flags `--dry-run`, `--json`. Reporte : scanned / normalized / kept-absolute / skipped.
**VERIFY** Test sur copie DB seedée. QA réel : `AISYNC_DATABASE_PATH=/tmp/copy.db aisync backfill normalize-paths --dry-run` puis exécution, puis requêtes de vérif (anti double-stripping = 0 ; distribution ; anti-null inchangé ; spot-check 10 lignes).

### Task 5 — Docs  [dépend de Task 2-4]
**WHERE** `architecture/blame.md` (corriger L183), `README.md` si besoin
**WHAT** Documenter : modèle relatif/absolu, discriminant project_path, dual-format query, commande backfill. Supprimer la fausse affirmation actuelle (`architecture/blame.md` L183 « CLI normalizes the input path before querying »).
**VERIFY** (étapes concrètes + résultats attendus) :
1. `grep -n "normalizes the input path" architecture/blame.md` → **0 résultat** (ancienne affirmation supprimée).
2. `grep -niE "relativ|relative-to-project|project_path" architecture/blame.md` → **≥1 résultat** décrivant le modèle relatif/absolu et le discriminant project_path.
3. `grep -n "backfill normalize-paths" architecture/blame.md README.md` → **≥1 résultat** (commande documentée).
4. `grep -niE "dual|both forms|absolu.*relat|relat.*absolu" architecture/blame.md` → **≥1 résultat** décrivant la query dual-format.

### Final Wave
- **F1 — Plan Compliance Audit** (`oracle`) : conformité au plan + idempotence backfill + non-régression blame + couverture du gap pull.

---

## Risks & Mitigations
- **Sync branch pré-migration** : un teammate qui pull des sessions à paths absolus → normalisé à l'import grâce à Task 3 (Save couvre pull). Idempotent.
- **Mix relatif/absolu pendant transition** : Task 2 matche les deux → aucune fenêtre cassée.
- **Hors-projet 13%** : restent absolus par design, toujours blamables localement.
- **Backfill sur 2.6GB DB** : `--dry-run` d'abord, sur copie via `AISYNC_DATABASE_PATH`, batch 1000.

## Out of Scope
- Auto-migration au boot (rejetée : pas de table de version).
- Résolution de symlinks.
- Re-réécriture de la branche sync existante (les sessions se renormalisent au prochain pull).
