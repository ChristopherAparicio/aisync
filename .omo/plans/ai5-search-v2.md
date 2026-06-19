# Refonte du search AI5 — Index user + résumés de compaction uniquement

## TL;DR

> **Quick Summary**: Réindexer le FTS5 d'AI5 en ne nourrissant l'index QUE des messages user + des résumés de compaction (drop assistant non-résumé + tool noise), afin d'augmenter la densité de signal (~6,6×) et la précision de récupération, sans perdre la couverture des sessions.
>
> **Deliverables**:
> - Champ `Message.IsCompactionSummary bool` + extraction du texte de résumé compaction (OpenCode)
> - `DocumentFromSession` filtré (user + résumés uniquement)
> - Harness d'évaluation precision@10 + fixture de requêtes figée avec IDs baseline
> - Réindex A/B sur copie de la vraie DB + rapport de gains/régressions
>
> **Estimated Effort**: Medium
> **Parallel Execution**: YES - 3 waves + wave finale
> **Critical Path**: T1 → T5/T6 → T7 → F1-F4 → okay user

---

## Context

### Original Request
« Rédiger le plan complet pour partir et améliorer le search AI5. » Faisant suite à une investigation A/B validée : l'index FTS5 actuel concatène user + assistant + sorties d'outils (bash/edit/write), ce qui noie le signal user sous le bruit machine et fausse le ranking bm25 (inflation de termes via échos d'outils).

### Interview Summary
**Key Discussions**:
- Indexer/vectoriser UNIQUEMENT messages user + résumés de compaction — « rien de plus. »
- Zéro dépendance OpenCode/externe au runtime du moteur search — local only.
- Garder TOUTES les sessions ; le recall brut (LIKE/grep, étage 1) reste sur le contenu complet.
- sqlite-vec / vectoriel = phase optionnelle séparée, HORS scope de ce plan.

**Research Findings** (vraie DB `~/.aisync/sessions.db`, 4958 sessions):
- Ratio user/total = 0,2–6,7 % → 93–99,8 % du blob indexé est du bruit.
- A/B clean vs noisy : clean = 15 % de la taille, signal 6,6× plus dense (5944 vs 39502 chars/session).
- Ranking « authentication » : noisy place `house` #1 (gonflé par échos d'outils ×5,4) ; clean le sort du top-10 et remonte trainercycle/Confide/admin-cli (auth = sujet central réel).
- Compaction OpenCode : part `type='compaction'` = simple marqueur `{type, auto}` SANS texte. Le résumé = texte du **prochain message assistant** (text part). 11 214 marqueurs ; échantillon 300 → 98 % trouvés, 98 % commencent par « ## Goal », médiane ~10 KB.
- Corpus = 100 % OpenCode sur cette machine (0 JSONL Claude). Le filtre document reste néanmoins provider-agnostic.

### Metis Review
**Identified Gaps** (auto-dérivés — Metis n'a pas retourné de texte actionnable ; gaps issus de l'investigation) :
- Risque de sous-extraction du signal user (OpenCode : 1 user msg observé / 16-31 assistant) → traité par un spike d'éval dédié (T4).
- Les 2 % de marqueurs compaction sans résumé suivant → skip gracieux exigé (T5).
- Dropper les sorties d'outils ne doit PAS régresser les recherches légitimes par chemin de fichier / commande → l'éval DOIT inclure ces requêtes (T2/T3/T7).
- Découvrabilité par fichier conservée hors FTS via `aisync blame` + table `file_changes`.

---

## Work Objectives

### Core Objective
Augmenter la précision de récupération du search AI5 en restreignant le corpus indexé FTS5 aux messages user et aux résumés de compaction, tout en garantissant zéro régression sur les recherches par projet et par chemin/commande.

### Concrete Deliverables
- `internal/session/session.go` : champ `Message.IsCompactionSummary bool`
- `internal/provider/opencode/dbreader.go` : extraction du texte de résumé compaction + marquage `IsCompactionSummary`
- `internal/search/document.go` : `DocumentFromSession` filtré (user + résumés uniquement, drop tool/assistant)
- `testdata/eval_queries.json` (chemin exact à confirmer en T2) : fixture de requêtes d'éval + IDs baseline attendus
- Harness d'évaluation precision@10 (test Go) comparant clean vs noisy
- Rapport A/B post-réindex avec mesures de gains et liste de régressions (zéro toléré sur requêtes chemin/commande)

### Definition of Done
- [ ] `make test` → PASS (aucune régression sur les 1456 tests existants)
- [ ] Harness d'éval exécuté : clean precision@10 ≥ noisy sur requêtes domaine/projet
- [ ] ZÉRO régression sur requêtes chemin-fichier/commande (couvertes par `blame`/`file_changes` ou contenu user)
- [ ] Taille d'index réduite (~85 % plus petit, cible 15 % de l'actuel) — mesurée et rapportée

### Must Have
- Index FTS5 nourri exclusivement de : `Role==user` + messages `IsCompactionSummary==true`.
- Extraction compaction robuste aux 2 % sans résumé suivant (skip propre, pas de crash).
- Filtre `DocumentFromSession` provider-agnostic (opère sur `Message.Role` + `IsCompactionSummary`).
- Fixture de requêtes d'éval figée avec IDs baseline, versionnée dans le repo.
- Réindex testé sur **copie** de la vraie DB (jamais en place sur `~/.aisync/sessions.db`).

### Must NOT Have (Guardrails)
- NE PAS dépendre d'OpenCode au runtime du moteur search — uniquement AI5.
- NE PAS changer les colonnes du schema FTS5 ni les poids bm25 (`engine.go:26-49`) sauf si l'éval le réclame — et alors documenter explicitement le changement.
- NE PAS toucher au chemin recall/LIKE (étage 1) : le grep continue sur le contenu complet.
- NE PAS casser les providers non-OpenCode (Claude/Cursor/Parlay/Ollama) : extraction compaction OpenCode-specific, mais filtre document universel.
- NE PAS inclure sqlite-vec/vectoriel dans ce plan (phase optionnelle séparée).
- NE PAS refondre la surface de commandes CLI ni ajouter de nouveaux providers.

---

## Verification Strategy (MANDATORY)

> **ZÉRO INTERVENTION HUMAINE** — toute la vérification est exécutée par l'agent.

### Test Decision
- **Infrastructure exists**: YES (Go testing, 1456 tests / 87 packages, `make test`)
- **Automated tests**: YES (TDD) — chaque tâche RED → GREEN avec table tests Go
- **Framework**: Go `testing` (`go test ./...` / `make test`)
- **If TDD**: tests d'abord (RED), implémentation minimale (GREEN), refactor

### QA Policy
Chaque tâche inclut des scénarios QA exécutés par l'agent. Evidence dans `.omo/evidence/task-{N}-{slug}.{ext}`.

- **Library/Module (Go)**: Bash — `go test`, build binaire, exécution sur DB copiée en tmp
- **CLI**: interactive_bash (tmux) ou Bash — `aisync search`/`list --search` sur DB tmp, capture sortie
- **Data/DB**: Bash — `sqlite3` sur copie DB, comparaison tailles index et rankings

---

## Execution Strategy

### Parallel Execution Waves

```
Wave 1 (Start Immediately — fondations + fixtures + spike):
├── Task 1: Champ Message.IsCompactionSummary [quick]
├── Task 2: Fixture eval_queries.json + IDs baseline [unspecified-high]
├── Task 3: Harness d'éval precision@10 [deep]
└── Task 4: Spike consolidation tours user OpenCode [unspecified-high]

Wave 2 (After Wave 1 — extraction + filtre, dépend T1; T5 informé par T4):
├── Task 5: Extraction texte résumé compaction OpenCode (dbreader.go) [deep]
└── Task 6: Filtre DocumentFromSession (user + IsCompactionSummary only) [deep]

Wave 3 (After Wave 2 — réindex + éval, dépend T2,T3,T5,T6):
└── Task 7: Réindex copie DB + run A/B éval + assertion seuils + rapport [deep]

Wave FINAL (After ALL — 4 reviews parallèles, puis okay user):
├── Task F1: Plan compliance audit (oracle)
├── Task F2: Code quality review (unspecified-high)
├── Task F3: Real manual QA (unspecified-high)
└── Task F4: Scope fidelity check (deep)
-> Présenter résultats -> okay explicite user

Critical Path: T1 → T5/T6 → T7 → F1-F4 → okay
Max Concurrent: 4 (Wave 1)
```

### Dependency Matrix

- **1**: dépend de — | bloque 5, 6
- **2**: dépend de — | bloque 7
- **3**: dépend de — | bloque 7
- **4**: dépend de — | bloque 5 (informatif)
- **5**: dépend de 1 (+ T4 informatif) | bloque 7
- **6**: dépend de 1 | bloque 7
- **7**: dépend de 2, 3, 5, 6 | bloque F1-F4
- **F1-F4**: dépend de 7 | bloque okay user

### Agent Dispatch Summary

- **Wave 1**: 4 — T1 → `quick`, T2 → `unspecified-high`, T3 → `deep`, T4 → `unspecified-high`
- **Wave 2**: 2 — T5 → `deep`, T6 → `deep`
- **Wave 3**: 1 — T7 → `deep`
- **FINAL**: 4 — F1 → `oracle`, F2 → `unspecified-high`, F3 → `unspecified-high`, F4 → `deep`

---

## TODOs

- [x] 1. Ajouter le flag `IsCompactionSummary` à la struct `Message`

  **What to do**:
  - Ajouter le champ `IsCompactionSummary bool \`json:"is_compaction_summary,omitempty"\`` à la struct `Message` dans `internal/session/session.go` (après `Role`, ligne ~123).
  - Vérifier la sérialisation/désérialisation JSON (round-trip) : un message marqué reste marqué après marshal+unmarshal.
  - RED : écrire un test de round-trip JSON dans `internal/session/session_test.go` (ou fichier de test existant) asserttant `IsCompactionSummary` préservé.
  - GREEN : ajouter le champ, faire passer le test.

  **Must NOT do**:
  - Ne PAS modifier d'autres champs de `Message` ni l'ordre des champs existants.
  - Ne PAS changer `MessageRole` ni les constantes de rôle.

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: Ajout d'un seul champ + test de round-trip, changement trivial et localisé.
  - **Skills**: []
    - Aucune skill requise (édition Go simple).

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (avec T2, T3, T4)
  - **Blocks**: T5, T6
  - **Blocked By**: None (peut démarrer immédiatement)

  **References**:

  **Pattern/Type References**:
  - `internal/session/session.go:115-132` — struct `Message`. Insérer le nouveau champ bool en suivant le style des tags JSON existants (`omitempty` pour les optionnels).
  - `internal/session/session.go:123` — champ `Role MessageRole` : placer le nouveau flag juste après pour la cohérence sémantique.

  **Test References**:
  - Chercher le fichier de test de session existant (`internal/session/*_test.go`) pour copier le pattern de table tests + marshal/unmarshal JSON.

  **WHY Each Reference Matters**:
  - La struct `Message` est sérialisée en SQLite (stockage sessions) ; un tag JSON incohérent casserait la persistance. Suivre exactement le style des champs voisins.

  **Acceptance Criteria**:

  **If TDD**:
  - [ ] Test round-trip ajouté dans `internal/session/session_test.go`
  - [ ] `go test ./internal/session/...` → PASS

  **QA Scenarios**:

  ```
  Scenario: Round-trip JSON préserve le flag
    Tool: Bash (go test)
    Preconditions: champ ajouté à Message
    Steps:
      1. Construire un Message{Role: user, IsCompactionSummary: true}
      2. json.Marshal puis json.Unmarshal
      3. Asserter résultat.IsCompactionSummary == true
    Expected Result: test PASS, flag préservé
    Failure Indicators: flag false après round-trip, ou erreur de compilation
    Evidence: .omo/evidence/task-1-roundtrip.txt

  Scenario: Compatibilité ascendante (champ absent du JSON)
    Tool: Bash (go test)
    Preconditions: JSON legacy sans la clé is_compaction_summary
    Steps:
      1. Unmarshal d'un JSON Message sans la clé
      2. Asserter IsCompactionSummary == false (zéro-value, pas d'erreur)
    Expected Result: désérialisation OK, défaut false
    Evidence: .omo/evidence/task-1-backcompat.txt
  ```

  **Evidence to Capture**:
  - [ ] `.omo/evidence/task-1-roundtrip.txt`, `.omo/evidence/task-1-backcompat.txt`

  **Commit**: YES
  - Message: `feat(session): add IsCompactionSummary flag to Message`
  - Files: `internal/session/session.go`, `internal/session/session_test.go`
  - Pre-commit: `go test ./internal/session/...`

- [x] 2. Créer la fixture de requêtes d'éval + IDs baseline attendus

  **What to do**:
  - Créer une fixture versionnée de requêtes d'évaluation (chemin proposé : `internal/search/testdata/eval_queries.json` — confirmer l'emplacement testdata du package search).
  - Chaque entrée : `{ "query": str, "category": "domain"|"project"|"path"|"command", "expected_session_ids": [string], "note": str }`.
  - Inclure au moins : requêtes domaine (ex. « authentication »), requêtes projet (ex. « omogen », « cycloplan », « aisync »), ET requêtes chemin-fichier/commande (ex. un path édité connu, une commande bash connue) — ces dernières servent à prouver l'absence de régression du drop tool-output.
  - Renseigner les `expected_session_ids` à partir de la vraie DB (`~/.aisync/sessions.db`, copiée en tmp) : pour chaque requête, identifier les sessions réellement pertinentes (vérité terrain), pas le ranking actuel.
  - Documenter dans le JSON la provenance des baseline IDs (commentaire `note` par requête).

  **Must NOT do**:
  - Ne PAS dériver les `expected_session_ids` du ranking noisy actuel (ce serait circulaire) — utiliser la pertinence réelle (contenu user de la session).
  - Ne PAS modifier `~/.aisync/sessions.db` ; travailler sur une copie tmp.

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: Nécessite du jugement pour établir une vérité terrain de pertinence à partir de la vraie DB, pas une tâche mécanique.
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (avec T1, T3, T4)
  - **Blocks**: T7
  - **Blocked By**: None

  **References**:

  **Pattern References**:
  - `internal/search/` — repérer un répertoire `testdata/` existant ou la convention de fixtures du package pour placer le JSON au bon endroit.
  - `~/.aisync/sessions.db` (copie tmp) — source des sessions réelles ; utiliser `sqlite3` pour inspecter `sessions` et le contenu user afin d'établir la pertinence.

  **WHY Each Reference Matters**:
  - La fixture est le contrat d'évaluation : sans vérité terrain indépendante du ranking actuel, le harness T3/T7 ne peut pas prouver un gain réel.

  **Acceptance Criteria**:
  - [ ] Fichier fixture créé avec ≥ 8 requêtes couvrant les 4 catégories (domain/project/path/command)
  - [ ] Chaque requête a ≥ 1 `expected_session_id` justifié par `note`
  - [ ] `go test ./internal/search/...` ne casse pas (fixture valide JSON, parsée par un test minimal)

  **QA Scenarios**:

  ```
  Scenario: La fixture est un JSON valide et parsable
    Tool: Bash (go test + jq)
    Preconditions: fixture créée
    Steps:
      1. jq . internal/search/testdata/eval_queries.json (valide la syntaxe)
      2. go test parse la fixture sans erreur
      3. Asserter présence des 4 catégories
    Expected Result: parsing OK, 4 catégories présentes
    Evidence: .omo/evidence/task-2-fixture-valid.txt

  Scenario: Les baseline IDs existent réellement dans la DB
    Tool: Bash (sqlite3)
    Preconditions: copie tmp de sessions.db
    Steps:
      1. Pour chaque expected_session_id, SELECT 1 FROM sessions WHERE id=?
      2. Asserter que chaque ID existe
    Expected Result: 100% des IDs baseline existent
    Failure Indicators: un ID baseline absent de la DB
    Evidence: .omo/evidence/task-2-ids-exist.txt
  ```

  **Evidence to Capture**:
  - [ ] `.omo/evidence/task-2-fixture-valid.txt`, `.omo/evidence/task-2-ids-exist.txt`

  **Commit**: YES
  - Message: `test(search): add eval query fixture with baseline IDs`
  - Files: `internal/search/testdata/eval_queries.json`
  - Pre-commit: `go test ./internal/search/...`

- [x] 3. Construire le harness d'évaluation precision@10

  **What to do**:
  - Écrire un test Go (`internal/search/eval_test.go`) qui : charge la fixture (T2), indexe un corpus dans deux moteurs FTS5 (un « noisy » = `DocumentFromSession` actuel, un « clean » = version filtrée), exécute chaque requête, et calcule precision@10 par catégorie.
  - Le harness doit être paramétrable sur la fonction de construction de document (injecter la fonction document-builder) pour comparer noisy vs clean sans dupliquer la logique d'indexation.
  - Pour cette tâche (avant que T6 existe), structurer le harness pour qu'il accepte deux builders ; le builder « clean » peut être un stub temporaire (ou skip propre) — l'objectif est le squelette de mesure, pas encore le filtre réel.
  - Exposer une fonction de calcul `precisionAtK(results, expected, k)` testée unitairement.
  - RED : tests sur `precisionAtK` (cas connus : 3/10, 0/10, 10/10). GREEN : implémenter.

  **Must NOT do**:
  - Ne PAS coder en dur les IDs attendus dans le harness (ils viennent de la fixture T2).
  - Ne PAS indexer `~/.aisync/sessions.db` en place ; utiliser une DB tmp / corpus de test.
  - Ne PAS dépendre de T5/T6 pour compiler : le builder clean est injecté/stubé.

  **Recommended Agent Profile**:
  - **Category**: `deep`
    - Reason: Conception d'un harness de mesure réutilisable, injection de dépendance, calcul de métrique correct — logique nécessitant du jugement.
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (avec T1, T2, T4)
  - **Blocks**: T7
  - **Blocked By**: None

  **References**:

  **Pattern References**:
  - `internal/search/fts5/engine.go:26-49` — schema FTS5 + poids bm25 ; le harness instancie le moteur via l'API publique existante, sans modifier ces poids.
  - `internal/search/document.go:13` — signature `DocumentFromSession(sess, maxContentLen)` : le builder « noisy » l'appelle directement ; le builder « clean » sera la variante filtrée (T6).
  - `internal/service/session_index.go:54` — pattern d'indexation (`DocumentFromSession` → `searchEngine.Index`) à reproduire dans le harness.

  **Test References**:
  - Tests existants du package `internal/search/fts5` pour le pattern d'instanciation d'un moteur FTS5 en mémoire/tmp.

  **WHY Each Reference Matters**:
  - Réutiliser l'API moteur existante garantit que l'éval mesure le vrai comportement de production, pas une réimplémentation divergente.

  **Acceptance Criteria**:
  - [ ] `precisionAtK` testé unitairement (≥ 3 cas)
  - [ ] Harness compile et tourne avec builder noisy réel + builder clean stub
  - [ ] `go test ./internal/search/...` → PASS

  **QA Scenarios**:

  ```
  Scenario: precisionAtK calcule correctement
    Tool: Bash (go test)
    Preconditions: harness écrit
    Steps:
      1. Appeler precisionAtK avec 3 hits sur 10, k=10 → asserter 0.3
      2. Cas 0 hit → asserter 0.0
      3. Cas tous pertinents → asserter 1.0
    Expected Result: 3 assertions PASS
    Evidence: .omo/evidence/task-3-precision-unit.txt

  Scenario: Le harness exécute une requête de bout en bout
    Tool: Bash (go test)
    Preconditions: corpus de test indexé (noisy builder)
    Steps:
      1. Indexer un petit corpus déterministe
      2. Exécuter une requête de la fixture
      3. Asserter qu'un score precision@10 numérique est produit (pas de crash)
    Expected Result: score produit, test PASS
    Failure Indicators: panic, score NaN, moteur non instancié
    Evidence: .omo/evidence/task-3-harness-e2e.txt
  ```

  **Evidence to Capture**:
  - [ ] `.omo/evidence/task-3-precision-unit.txt`, `.omo/evidence/task-3-harness-e2e.txt`

  **Commit**: YES
  - Message: `test(search): add precision@10 eval harness`
  - Files: `internal/search/eval_test.go`
  - Pre-commit: `go test ./internal/search/...`

- [x] 4. Spike : vérifier la consolidation des tours user OpenCode

  **What to do**:
  - Investiguer (lecture + requêtes `sqlite3` sur `opencode.db` et `~/.aisync/sessions.db` copiée en tmp) si OpenCode fusionne/consolide plusieurs tours user en un seul `Message{Role:user}` côté AI5, ou si chaque tour user produit un message distinct.
  - Quantifier sur un échantillon (≥ 30 sessions) : nb moyen de messages `Role==user` par session, longueur moyenne, et si le contenu user multi-tours est concaténé ou perdu.
  - Conclure : le drop assistant fait-il perdre du signal user légitime ? Si OUI, recommander une stratégie (ex. inclure aussi un champ user dérivé) ; si NON, confirmer que user-only suffit.
  - Produire un court rapport markdown dans `.omo/evidence/` (PAS de code de prod).

  **Must NOT do**:
  - Ne PAS modifier de code de production dans cette tâche (spike d'investigation uniquement).
  - Ne PAS modifier les DB sources ; copies tmp en lecture.

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: Investigation empirique avec synthèse et recommandation — jugement requis, pas mécanique.
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (avec T1, T2, T3)
  - **Blocks**: T5 (informatif — oriente la stratégie d'extraction)
  - **Blocked By**: None

  **References**:

  **Pattern References**:
  - `internal/provider/opencode/dbreader.go` — `loadAllPartsForSession` / `loadParts` : comprendre comment les parts user/assistant sont mappées en `Message`.
  - `internal/session/session.go:115-132` — struct `Message` cible pour comprendre ce qui est conservé après mapping.
  - `~/.aisync/sessions.db` + `opencode.db` (copies tmp) — données réelles pour l'échantillon.

  **WHY Each Reference Matters**:
  - L'hypothèse « 1 user msg / 16-31 assistant » observée en investigation doit être confirmée : si OpenCode écrase des tours user, l'index user-only sous-représenterait le signal et T5/T6 devraient compenser.

  **Acceptance Criteria**:
  - [ ] Rapport `.omo/evidence/task-4-user-consolidation.md` avec stats sur ≥ 30 sessions
  - [ ] Conclusion explicite : user-only suffisant OUI/NON + recommandation pour T5/T6

  **QA Scenarios**:

  ```
  Scenario: Stats de tours user produites sur échantillon réel
    Tool: Bash (sqlite3)
    Preconditions: copie tmp des DB
    Steps:
      1. Échantillonner ≥ 30 sessions OpenCode
      2. Compter messages Role==user par session, longueur contenu
      3. Comparer au contenu brut OpenCode (parts user) pour détecter perte
    Expected Result: tableau de stats + verdict perte/non-perte
    Failure Indicators: échantillon < 30, pas de verdict
    Evidence: .omo/evidence/task-4-user-consolidation.md
  ```

  **Evidence to Capture**:
  - [ ] `.omo/evidence/task-4-user-consolidation.md`

  **Commit**: YES
  - Message: `chore(eval): spike report on OpenCode user-turn consolidation`
  - Files: `.omo/evidence/task-4-user-consolidation.md`
  - Pre-commit: n/a (pas de code de prod)

- [x] 5. Extraction du texte de résumé de compaction (OpenCode)

  **What to do**:
  - Dans `internal/provider/opencode/dbreader.go`, détecter les marqueurs de compaction : message user porteur d'un part `type='compaction'` (marqueur `{type, auto}` sans texte).
  - Pour chaque marqueur, extraire le texte du **prochain message assistant** (text part) = le résumé structuré. Marquer le `Message` correspondant avec `IsCompactionSummary = true` (champ ajouté en T1).
  - Gérer les 2 % de marqueurs SANS message assistant suivant : skip gracieux (pas de panic, pas de message vide marqué).
  - Gérer plusieurs marqueurs de compaction dans une même session : extraire chacun.
  - Intégrer la recommandation du spike T4 (si T4 conclut à une perte de signal user, l'extraction doit en tenir compte — sinon, user + résumés uniquement).
  - RED : tests table dans `dbreader_test.go` couvrant : 1 marqueur + résumé, marqueur sans suivant, marqueurs multiples, session non-compactée.
  - GREEN : implémenter l'extraction.

  **Must NOT do**:
  - Ne PAS marquer comme résumé un message assistant non précédé d'un marqueur compaction.
  - Ne PAS crasher sur marqueur orphelin (2 %) ni sur session vide.
  - Ne PAS introduire de dépendance OpenCode dans le moteur search lui-même : l'extraction vit dans le provider OpenCode ; le moteur ne voit que `Message.IsCompactionSummary`.

  **Recommended Agent Profile**:
  - **Category**: `deep`
    - Reason: Logique d'extraction stateful (corrélation marqueur → next-assistant-part), edge cases multiples, parsing DB — jugement requis.
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES (avec T6, en parallèle dans Wave 2)
  - **Parallel Group**: Wave 2 (avec T6)
  - **Blocks**: T7
  - **Blocked By**: T1 (champ requis) ; informé par T4

  **References**:

  **Pattern References**:
  - `internal/provider/opencode/dbreader.go` — `loadAllPartsForSession` / `loadParts` : point d'insertion ; comprendre l'ordre des parts et le mapping part→Message.
  - `internal/session/session.go:123` — champ `IsCompactionSummary` (T1) à positionner sur le Message assistant résumé.
  - `opencode.db` (copie tmp) — données réelles : part `type='compaction'` (marqueur `{type, auto}`), 11 214 marqueurs ; le résumé suit dans le prochain assistant text part (98 % commencent par « ## Goal »).

  **Test References**:
  - Tests existants `internal/provider/opencode/*_test.go` pour le pattern de fixture DB / mock parts.

  **WHY Each Reference Matters**:
  - L'heuristique « next assistant text part = résumé » est validée à 98 % empiriquement ; l'implémentation doit reproduire exactement cette corrélation et gérer les 2 % restants proprement.

  **Acceptance Criteria**:

  **If TDD**:
  - [ ] Tests table dans `dbreader_test.go` (4 cas : nominal, orphelin, multiples, non-compacté)
  - [ ] `go test ./internal/provider/opencode/...` → PASS

  **QA Scenarios**:

  ```
  Scenario: Résumé de compaction extrait et marqué (nominal)
    Tool: Bash (go test)
    Preconditions: fixture avec 1 marqueur compaction + assistant suivant "## Goal ..."
    Steps:
      1. Charger la session via dbreader
      2. Trouver le Message assistant suivant le marqueur
      3. Asserter IsCompactionSummary == true ET Content commence par "## Goal"
    Expected Result: 1 message marqué, contenu = résumé
    Evidence: .omo/evidence/task-5-extract-nominal.txt

  Scenario: Marqueur orphelin sans assistant suivant (skip gracieux)
    Tool: Bash (go test)
    Preconditions: fixture marqueur compaction en dernier, sans assistant après
    Steps:
      1. Charger la session
      2. Asserter aucun crash, 0 message marqué à tort
    Expected Result: pas de panic, aucun Message vide marqué
    Failure Indicators: panic, message vide avec IsCompactionSummary=true
    Evidence: .omo/evidence/task-5-extract-orphan.txt

  Scenario: Extraction sur vraie DB (échantillon)
    Tool: Bash (go run/test sur copie tmp)
    Preconditions: copie tmp opencode.db
    Steps:
      1. Extraire sur 300 sessions à marqueurs
      2. Asserter taux de résumés trouvés ~98%
    Expected Result: ~98% marqueurs résolus, reste skipé proprement
    Evidence: .omo/evidence/task-5-extract-realdb.txt
  ```

  **Evidence to Capture**:
  - [ ] `.omo/evidence/task-5-extract-nominal.txt`, `-orphan.txt`, `-realdb.txt`

  **Commit**: YES
  - Message: `feat(opencode): extract compaction summary text`
  - Files: `internal/provider/opencode/dbreader.go`, `internal/provider/opencode/dbreader_test.go`
  - Pre-commit: `go test ./internal/provider/opencode/...`

- [x] 6. Filtrer `DocumentFromSession` : user + résumés uniquement

  **What to do**:
  - Modifier `internal/search/document.go` (`DocumentFromSession`, lignes 34-94) pour ne concaténer dans `doc.Content` QUE : messages `Role==user` ET messages `IsCompactionSummary==true`.
  - Supprimer du contenu indexé : texte assistant non-résumé, inputs/outputs d'outils (bash/edit/write), thinking.
  - Décision à acter : conserver ou non `doc.ToolNames` (liste de noms d'outils, sans contenu) — par défaut CONSERVER `ToolNames` (signal léger, pas du bruit de contenu), mais NE PLUS indexer les inputs/outputs d'outils dans `Content`. Documenter ce choix dans un commentaire.
  - Garder le respect de `maxContentLen` (troncature).
  - Le filtre opère sur `Message.Role` + `Message.IsCompactionSummary` uniquement → provider-agnostic (fonctionne pour Claude/Cursor/Parlay/Ollama même sans extraction compaction).
  - RED : tests dans `document_test.go` : session avec user+assistant+tools → `Content` ne contient que le texte user + résumés ; assertion d'absence de commandes bash / contenu assistant non-résumé.
  - GREEN : appliquer le filtre.

  **Must NOT do**:
  - Ne PAS changer la signature de `DocumentFromSession` (appelée en `session_index.go:54,79`).
  - Ne PAS changer le schema FTS5 ni les poids bm25 (`engine.go:26-49`).
  - Ne PAS toucher au chemin recall/LIKE (étage 1).
  - Ne PAS supprimer les champs métadonnées du Document (Summary, Branch, ProjectPath, etc.).

  **Recommended Agent Profile**:
  - **Category**: `deep`
    - Reason: Changement au cœur de la pertinence search avec invariants à préserver (signature, métadonnées, provider-agnosticité) — jugement requis.
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES (avec T5, en parallèle dans Wave 2)
  - **Parallel Group**: Wave 2 (avec T5)
  - **Blocks**: T7
  - **Blocked By**: T1 (champ requis)

  **References**:

  **Pattern References**:
  - `internal/search/document.go:34-94` — boucle de concaténation actuelle (user+assistant+tool inputs/outputs) ; c'est exactement ce qui doit être restreint à user + IsCompactionSummary.
  - `internal/search/document.go:54-91` — bloc d'indexation des ToolCalls (bash/Edit/Write) à retirer du `Content` (mais conserver la collecte de `toolNames` si on garde `ToolNames`).
  - `internal/service/session_index.go:54` et `:79` — appelants de `DocumentFromSession` : la signature DOIT rester inchangée.
  - `internal/session/session.go:123` — `IsCompactionSummary` (T1), condition d'inclusion.

  **Test References**:
  - `internal/search/document_test.go` (s'il existe) pour le pattern de construction de session de test + assertions sur `doc.Content`.

  **WHY Each Reference Matters**:
  - L'investigation A/B prouve que c'est ce filtre précis qui densifie le signal 6,6× et corrige le ranking ; toute déviation (garder du tool-output) annulerait le gain.

  **Acceptance Criteria**:

  **If TDD**:
  - [ ] Tests dans `document_test.go` : Content = user + résumés seulement, 0 contenu tool/assistant non-résumé
  - [ ] `go test ./internal/search/...` → PASS
  - [ ] Signature `DocumentFromSession` inchangée (compile sans modifier les appelants)

  **QA Scenarios**:

  ```
  Scenario: Content ne contient que user + résumés
    Tool: Bash (go test)
    Preconditions: session de test {user:"how to add auth", assistant:"sure...", tool bash:"ls -la", assistant compaction-summary:"## Goal auth"}
    Steps:
      1. Appeler DocumentFromSession
      2. Asserter Content contient "how to add auth" ET "## Goal auth"
      3. Asserter Content NE contient PAS "sure..." NI "ls -la"
    Expected Result: contenu filtré, tool/assistant non-résumé absents
    Failure Indicators: présence de "ls -la" ou texte assistant non-résumé
    Evidence: .omo/evidence/task-6-filter-content.txt

  Scenario: Provider non-OpenCode dégrade proprement (user-only)
    Tool: Bash (go test)
    Preconditions: session Claude/Cursor sans IsCompactionSummary
    Steps:
      1. Appeler DocumentFromSession sur session sans résumés marqués
      2. Asserter Content = concat des messages user, pas de crash
    Expected Result: user-only indexé, aucune erreur
    Evidence: .omo/evidence/task-6-filter-nonopencode.txt

  Scenario: maxContentLen respecté
    Tool: Bash (go test)
    Preconditions: session avec contenu user > maxContentLen
    Steps:
      1. Appeler avec maxContentLen petit (ex. 100)
      2. Asserter len(Content) <= 100
    Expected Result: troncature respectée
    Evidence: .omo/evidence/task-6-filter-maxlen.txt
  ```

  **Evidence to Capture**:
  - [ ] `.omo/evidence/task-6-filter-content.txt`, `-nonopencode.txt`, `-maxlen.txt`

  **Commit**: YES
  - Message: `feat(search): index user + compaction summaries only`
  - Files: `internal/search/document.go`, `internal/search/document_test.go`
  - Pre-commit: `go test ./internal/search/...`

- [x] 7. Réindex sur copie DB + run A/B éval + assertion seuils + rapport

  **What to do**:
  - Brancher le builder « clean » réel du harness (T3) sur le `DocumentFromSession` filtré (T6) — remplacer le stub par l'implémentation effective.
  - Sur une **copie tmp** de `~/.aisync/sessions.db` : indexer le corpus complet en deux moteurs (noisy = builder actuel, clean = builder filtré) en utilisant l'extraction compaction (T5).
  - Exécuter toutes les requêtes de la fixture (T2) sur les deux moteurs, calculer precision@10 par catégorie.
  - Assertions (le test échoue si non tenues) :
    - clean precision@10 ≥ noisy sur les catégories `domain` et `project` (agrégé).
    - ZÉRO régression sur catégories `path` et `command` : chaque requête path/command garde ses `expected_session_ids` dans le top-10 clean (ou prouver la couverture via `blame`/`file_changes`).
  - Mesurer et rapporter : taille d'index clean vs noisy (cible ~15 %), temps de réindex (mesuré, borne documentée).
  - Produire un rapport `.omo/evidence/task-7-ab-report.md` : tableau precision@10 par catégorie, tailles, temps, liste de toute régression.

  **Must NOT do**:
  - Ne JAMAIS indexer/écrire sur `~/.aisync/sessions.db` en place — copie tmp obligatoire.
  - Ne PAS ajuster les poids bm25 pour « faire passer » l'éval sans le documenter ; si l'éval réclame un ajustement, le documenter explicitement dans le rapport et le signaler comme déviation au guardrail.
  - Ne PAS introduire sqlite-vec.

  **Recommended Agent Profile**:
  - **Category**: `deep`
    - Reason: Orchestration de l'éval bout-en-bout sur données réelles, interprétation des métriques, décision sur les seuils — jugement élevé.
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: NO (intègre T5+T6, consomme T2+T3)
  - **Parallel Group**: Wave 3 (seul)
  - **Blocks**: F1-F4
  - **Blocked By**: T2, T3, T5, T6

  **References**:

  **Pattern References**:
  - `internal/search/eval_test.go` (T3) — harness à compléter avec le builder clean réel.
  - `internal/search/document.go` (T6) — builder clean filtré.
  - `internal/provider/opencode/dbreader.go` (T5) — extraction compaction qui alimente le corpus clean.
  - `internal/service/session_index.go:41-66` — pattern de boucle d'indexation complète du corpus à reproduire pour le réindex A/B.
  - `internal/search/fts5/engine.go:26-49` — schema/bm25 INCHANGÉS (lecture pour instancier le moteur).
  - `~/.aisync/sessions.db` (copie tmp) — corpus réel (4958 sessions).

  **WHY Each Reference Matters**:
  - C'est la tâche de preuve : elle valide empiriquement que la refonte tient ses promesses (densité, ranking, taille) sans régression, sur la vraie distribution de données.

  **Acceptance Criteria**:
  - [ ] Test d'éval `go test ./internal/search/... -run Eval` → PASS avec assertions de seuil
  - [ ] clean precision@10 ≥ noisy (domain + project)
  - [ ] 0 régression path/command (top-10 conservé ou couverture blame/file_changes prouvée)
  - [ ] Rapport `.omo/evidence/task-7-ab-report.md` complet (precision, tailles, temps, régressions)
  - [ ] `make test` global → PASS

  **QA Scenarios**:

  ```
  Scenario: A/B éval sur vraie DB — clean >= noisy (domain/project)
    Tool: Bash (go test sur copie tmp)
    Preconditions: copie tmp sessions.db, T5/T6 mergés
    Steps:
      1. Indexer corpus en moteurs noisy + clean
      2. Exécuter requêtes fixture domain/project
      3. Asserter precision@10(clean) >= precision@10(noisy) agrégé
    Expected Result: clean >= noisy, assertion verte
    Failure Indicators: clean < noisy sur domain/project
    Evidence: .omo/evidence/task-7-ab-domain.txt

  Scenario: Zéro régression sur requêtes path/command
    Tool: Bash (go test)
    Preconditions: requêtes path/command de la fixture
    Steps:
      1. Exécuter chaque requête path/command sur clean
      2. Asserter expected_session_ids dans top-10 clean OU présents via blame/file_changes
    Expected Result: 0 régression
    Failure Indicators: un expected_id sort du top-10 sans couverture blame
    Evidence: .omo/evidence/task-7-ab-path.txt

  Scenario: Réduction de taille d'index mesurée
    Tool: Bash (sqlite3 / mesure)
    Preconditions: deux index construits
    Steps:
      1. Mesurer taille index clean vs noisy
      2. Asserter clean ~<= 20% de noisy (cible 15%)
    Expected Result: réduction ~85% confirmée
    Evidence: .omo/evidence/task-7-index-size.txt
  ```

  **Evidence to Capture**:
  - [ ] `.omo/evidence/task-7-ab-domain.txt`, `-ab-path.txt`, `-index-size.txt`, `task-7-ab-report.md`

  **Commit**: YES
  - Message: `test(search): A/B reindex eval + report`
  - Files: `internal/search/eval_test.go`, `.omo/evidence/task-7-ab-report.md`
  - Pre-commit: `make test`

---

## Final Verification Wave (MANDATORY — after ALL implementation tasks)

> 4 review agents run in PARALLEL. ALL must APPROVE. Présenter résultats consolidés à l'user et obtenir un « okay » explicite avant de clôturer.
> **Ne PAS auto-clôturer après vérification. Attendre l'approbation explicite.**
> **Ne jamais cocher F1-F4 avant l'okay user.** Rejet/feedback → fix → re-run → re-présenter → attendre okay.

- [x] F1. **Plan Compliance Audit** — `oracle`
  Lire le plan de bout en bout. Pour chaque « Must Have » : vérifier l'implémentation (lire fichier, lancer commande/test). Pour chaque « Must NOT Have » : grep le codebase pour les patterns interdits — rejeter avec file:line si trouvé (ex. dépendance OpenCode au runtime search, changement bm25 non documenté). Vérifier que les evidence existent dans `.omo/evidence/`.
  Output: `Must Have [N/N] | Must NOT Have [N/N] | Tasks [N/N] | VERDICT: APPROVE/REJECT`

- [x] F2. **Code Quality Review** — `unspecified-high`
  Lancer `make test` + `make lint` (golangci-lint) + `go build`. Revoir les fichiers modifiés : `as any` Go-équivalents (interface{} non justifié), erreurs ignorées (`_ =`), `panic` en prod, code commenté, imports inutilisés. AI slop : commentaires excessifs, sur-abstraction, noms génériques.
  Output: `Build [PASS/FAIL] | Lint [PASS/FAIL] | Tests [N pass/N fail] | Files [N clean/N issues] | VERDICT`

- [x] F3. **Real Manual QA** — `unspecified-high`
  Partir d'un état propre. Copier la vraie DB en tmp, builder le binaire, exécuter CHAQUE scénario QA de CHAQUE tâche — étapes exactes, capture evidence. Lancer des requêtes réelles (`aisync search` / `list --search`) domaine/projet ET chemin-fichier, comparer rankings clean vs baseline. Tester edge cases : session sans contenu user, non-OpenCode. Sauver dans `.omo/evidence/final-qa/`.
  Output: `Scenarios [N/N pass] | Path-queries [N/N no-regression] | Edge Cases [N tested] | VERDICT`

- [x] F4. **Scope Fidelity Check** — `deep`
  Pour chaque tâche : lire « What to do », lire le diff réel (git log/diff). Vérifier 1:1 — tout le spec construit (rien manquant), rien au-delà (pas de creep, notamment pas de sqlite-vec, pas de changement bm25/schema non réclamé par l'éval, pas de modif du chemin recall/LIKE). Vérifier conformité « Must NOT do ». Détecter contamination inter-tâches et changements non comptabilisés.
  Output: `Tasks [N/N compliant] | Contamination [CLEAN/N issues] | Unaccounted [CLEAN/N files] | VERDICT`

---

## Commit Strategy

- **T1**: `feat(session): add IsCompactionSummary flag to Message` — session.go, session_test.go ; pre: `go test ./internal/session/...`
- **T2**: `test(search): add eval query fixture with baseline IDs` — testdata/eval_queries.json ; pre: `go test ./internal/search/...`
- **T3**: `test(search): add precision@10 eval harness` — eval_test.go ; pre: `go test ./internal/search/...`
- **T4**: `chore(eval): spike report on OpenCode user-turn consolidation` — .omo/evidence/ (pas de commit code) ; pre: n/a
- **T5**: `feat(opencode): extract compaction summary text` — dbreader.go, dbreader_test.go ; pre: `go test ./internal/provider/opencode/...`
- **T6**: `feat(search): index user + compaction summaries only` — document.go, document_test.go ; pre: `go test ./internal/search/...`
- **T7**: `test(search): A/B reindex eval + report` — eval_test.go, .omo/evidence/ ; pre: `make test`

---

## Success Criteria

### Verification Commands
```bash
make test                                   # Expected: ok, 0 failures
make lint                                    # Expected: 0 issues
go test ./internal/search/... -run Eval -v   # Expected: precision@10 clean >= noisy, 0 path-query regression
```

### Final Checklist
- [ ] Tous les « Must Have » présents
- [ ] Tous les « Must NOT Have » absents
- [ ] Index nourri user + résumés uniquement (vérifié sur DB tmp)
- [ ] Tous les tests passent + lint clean
