# Draft: Architecture de recherche AI5 (index vs grep vs hybride)

## Question stratégique de l'utilisateur
> "On peut indexer ou simplement faire de la recherche par grep sans tout indexer non ?
> Essaye de réfléchir en profondeur à tous les use case et voir la meilleure architecture pour y répondre."

Décision de fond : **pré-indexation FTS5** vs **grep à la demande sur la source** vs **hybride**.

## Contexte (confirmé sessions précédentes)
- AI5 = `aisync` (Go), outil de recherche/observabilité de sessions agent.
- Deux DB :
  - AI5 DB `~/.aisync/sessions.db` (tables `sessions`, `sessions_fts`, etc.) — contient l'index FTS5.
  - OpenCode DB `~/.local/share/opencode/opencode.db` (source de vérité : `session`, `message`, `part` ; data JSON).
- Recherche actuelle = FTS5 SQLite, BM25 keyword, `Semantic: false`.

## Bug actuel (root cause déjà identifié)
1. `document.go` : `MaxContentLength=50000`, indexe depuis le DÉBUT des messages et stoppe à la limite → troncature.
2. Les compaction summaries (où l'info clé se trouve souvent) ne sont PAS indexées comme contenu de 1ère classe.
3. Pas de propagation child→parent : un child matche, le parent pertinent ne remonte pas.
4. `escapeFTS5` quote chaque mot → opérateur `OR` cassé (traité comme token littéral).

## Use cases à couvrir (à compléter/valider avec user)
1. Trouver la session qui a bossé sur projet/path X (métadonnée).
2. Trouver la session sur un sujet/topic Y (contenu keyword).
3. Trouver une phrase précise enfouie dans un message tardif (ex: "colle la clé Notion") — full-text profond.
4. Lister rapidement les sessions récentes (métadonnée, tri date).
5. Remonter le PARENT quand un child matche (traversée de relation).
6. Recherche globale cross-projet.
7. Analyse coût/tokens par session (agrégation, pas recherche).

## Options d'architecture (à arbitrer)
- **A. FTS5 index amélioré** : garder l'index mais indexer compactions + derniers N msgs + child→parent + fix OR.
- **B. Grep à la demande** : pas de pré-index, scan de la source au moment de la requête.
- **C. Hybride** : index léger (métadonnée + recall rapide) + grep ciblé pour le contenu profond/frais.

## Axes de décision
- Fraîcheur / staleness (l'index est-il à jour ? résumés stale ?).
- Latence à l'échelle (volume réel de messages/parts).
- Complexité de maintenance / sync entre 2 DB.
- Qualité du ranking (BM25 vs scan brut).
- Traversée child→parent.
- Le use case échoué : trouver le parent par contenu enfoui dans une compaction tardive.

## Volumes réels (mesurés 2026-06-01)
- **OpenCode DB (source)** : 2 196 sessions, **695 700 messages**, **2 620 611 parts**, fichier **11 Go**, ~**6 Go** de texte `part.data` (JSON).
- **AI5 DB** : 3 682 sessions, 3 664 lignes FTS, fichier **2,0 Go**.
- AI5 contient déjà des tables riches : `session_forks`, `session_links`, `session_session_links`, `session_analytics`, `token_usage_buckets`, `tool_usage_buckets`, `session_objectives`, `session_hotspots`, etc. (beaucoup d'infra déjà en place).

### Implication clé (grep brut)
- Grep/scan à la demande sur **6 Go de JSON** au moment de la requête = **non viable pour une recherche interactive** (latence multi-secondes, full table scan `part.data`).
- MAIS grep ciblé sur un **petit ensemble de sessions candidates** (déjà filtrées par index) = viable et précieux pour le use case "phrase enfouie tardive".
- => Pousse vers l'**hybride C** : index léger always-fresh pour le recall + grep profond ciblé à la demande. À confirmer par Oracle.

## Recherche en cours
- [x] Volumes réels — voir ci-dessus.
- [ ] Mécanisme sync/import actuel (incrémental vs full ; live vs copie) — explore bg_33d82890.
- [ ] Reco architecture Oracle — bg_da69c1a2.

## Reco Oracle (bg_da69c1a2) — Hybride C "metadata léger + grep à la demande"
- **Tier 1** : index métadonnée léger always-fresh (session_id, parent_id, project_path, title, timestamps, token_count, cost, has_compaction, child_ids). Couvre UC 1/4/5/6/7. Jamais tronqué, jamais stale.
- **Tier 2** : grep à la demande sur le **contenu** (UC 2/3), filtré aux sessions candidates de Tier 1 (cap N=200 ou exiger >=1 filtre métadonnée).
- **child→parent** = post-processing au query-time (walk `parent_id`), PAS une affaire d'indexation.
- **Drop** la duplication de contenu + `MaxContentLength` + table FTS content → supprime troncature ET staleness à la racine.
- **Fix OR** seulement si on garde un FTS (titres). Sinon le bug disparaît.
- Modes : `meta` / `keyword` / `phrase` + flag `include-parents`.
- Ordre : metadata index → parent propagation → grep on-demand → drop content dup. Compactions incluses gratuitement par le grep.
- Effort : Medium (1-2j).
- **Risques** : couplage au schéma OpenCode (DB qu'on ne possède pas → pin + version check), accès concurrent (ouvrir OpenCode en `?mode=ro` + WAL), latence grep si mot très commun sans filtre (cap candidats).
- Futur optionnel : FTS NARROW sur titres + compaction summaries (cheap, fresh, pas de troncature) si BM25 redevient utile.

## Carte du pipeline actuel (explore bg_33d82890) — NUANCES CRITIQUES
- **`session_export.go:Import` n'est PAS l'importeur OpenCode** : c'est un import raw générique. Le vrai ingest OpenCode = `setupcmd discoverAndImport` → `capture/service.go` → `provider/opencode.Export()`.
- Lecture OpenCode : `provider/opencode/dbreader.go:19-63` ouvre `opencode.db` (fallback fichiers legacy si absent).
- Capture **incrémental** après 1er export full (`capture/service.go:247-303`, skip-if-unchanged + `ExportIncremental`).
- **AI5 stocke le PAYLOAD COMPLET compressé** dans `sessions.payload` (`sqlite.go:202-252`) — donc le contenu est **DÉJÀ dupliqué**, pas juste de la métadonnée. (Oracle ne le savait pas.)
- FTS : indexation **per-session incrémentale** au write via hook post-capture (`factory/default.go:439-563` → `IndexSession`). `document.go:12-95` tronque à 50k.
- **Pas de cron de réindexation** ; seul `capture_all` est schedulé (`servecmd serve.go:495-563`).
- **Query path = AI5 DB/index UNIQUEMENT — OpenCode DB jamais lue au query-time** (`session_search.go:163-194`). Ajouter du grep = nouvelle capacité (lire OpenCode live OU grep le payload AI5 stocké).
- parent_id : colonne `sessions.parent_id` (`sqlite.go:21-37`), tree dans `session_crud.go:251-320`, enfants importés avec parent_id (`opencode.go:470-545`). + tables `session_links`, `session_forks`, `session_session_links` déjà présentes.

## DÉCISION-CLÉ qui émerge (3 variantes hybrides)
La cible du grep profond est le vrai arbitrage (Oracle supposait grep source ; explore montre que le payload est déjà local) :
- **V1 — Grep payload local AI5** : grep le `sessions.payload` décompressé des candidats. Autonome (zéro couplage OpenCode au query-time), contenu déjà là. Con : décompression au query-time, staleness possible si capture pas à jour (mais capture est auto/incrémental).
- **V2 — Grep source OpenCode live** : grep `part.data` de la DB OpenCode (read-only). Zéro staleness/troncature. Con : couplage schéma foreign + lecture sur 11 Go.
- **V3 — FTS5 corrigé + grep ciblé en complément** : indexe compactions + fenêtre complète (pas que 50k début) + fix OR ; garde BM25 pour recall rapide cross-session ; ajoute mode `--deep`/grep pour phrases enfouies. Plus incrémental, garde le ranking. Con : garde la complexité d'indexation.

## Décisions confirmées
- Direction générale = HYBRIDE (metadata index always-fresh + grep profond ciblé). Validé par Oracle.

## Questions ouvertes (à trancher avec user)
- Q1 : quelle variante de grep (V1 payload local / V2 source live / V3 FTS+grep) ?
- Q2 : stratégie de test (Go) — TDD / tests-après / agent QA only ?
- Q3 : garde-t-on un FTS NARROW (titres + compaction summaries) pour le recall rapide BM25, ou grep pur ?

## Clarification utilisateur (dernier échange)
L'utilisateur veut vérifier :
- Est-ce que grep fonctionne vraiment ?
- Est-ce qu'AI5 stocke toutes les sessions ?
- Peut-on grep directement dans AI5 sans dépendre d'OpenCode ?
- Si la DB AI5 devient trop grosse, comment compresser côté AI5 ?
- Quel est précisément le problème avec la réparation FTS5 ?

## Clarification utilisateur — autonomie AI5 vs OpenCode
Question utilisateur : "AI5 est complètement autonome sans OpenCode ? Je pensais qu'on stockait toutes les informations côté AI5."

Clarification à préserver :
- AI5 est **autonome après capture/import** : une session capturée est stockée dans AI5 (`sessions.payload`) et peut être cherchée/restaurée sans relire OpenCode au query-time.
- AI5 n'est **pas magiquement omniscient** : pour les nouvelles sessions non capturées, il doit encore lire le provider source (OpenCode DB/CLI) pendant l'ingestion/capture.
- La complétude dépend du **storage mode** (`full`, `compact`, `summary`) et de ce que le provider exporte. En mode full, objectif = conversation complète ; en compact/summary, le contenu est volontairement réduit.
- Donc l'objectif produit à planifier : AI5 = **source de vérité autonome pour toutes les sessions capturées**, avec grep local sur payload AI5. OpenCode ne doit être utilisé que comme source d'ingestion, jamais comme dépendance runtime de recherche.

## Décision confirmée — autonomie stricte AI5
- User confirme : **"faut absolument pas dépendre d'OpenCode mais uniquement de AI5"**.
- Règle produit confirmée : OpenCode est uniquement une **source d'ingestion/capture**, jamais une dépendance de recherche au query-time.
- Le plan doit donc cibler : recherche autonome sur `~/.aisync/sessions.db`, incluant grep profond du `sessions.payload` AI5 et non de `~/.local/share/opencode/opencode.db`.
- À vérifier/garantir dans le plan : complétude du payload AI5 selon storage mode (`full`/`compact`/`summary`) et fraîcheur capture/import.

Réponse à intégrer dans le plan :
- **Oui, grep peut fonctionner**, mais uniquement comme **grep ciblé** sur candidats filtrés (pas full scan global à chaque requête).
- **AI5 stocke déjà un payload complet compressé par session** dans `sessions.payload` pour les sessions capturées/importées. AI5 peut donc grep directement son propre payload, sans dépendre d'OpenCode au query-time.
- Limite : AI5 ne connaît que ce qu'il a capturé/importé. Si une session OpenCode n'a jamais été capturée ou si capture stale, grep AI5 ne la verra pas ; il faut donc renforcer fraîcheur/capture incrémentale.
- Compression : AI5 a déjà une compression de payload ; si la taille devient trop grosse, options : storage modes (`full`/`compact`/`summary`), zstd/blobs chunkés, tables de fragments compressés par session/message, cache de snippets/matchs, retention/archivage, VACUUM/partitionnement éventuel.
- Réparation FTS5 : le problème n'est pas FTS5 lui-même ; c'est **le document indexé** et le **query parser** actuels : troncature 50k début, compactions non prioritaires, pas de child→parent, opérateur OR cassé. Réparer FTS5 = plus de complexité et toujours un risque de staleness ; d'où préférence V1 + index léger + grep payload local.

## Scope
- INCLUDE: refonte recherche (index léger + grep ciblé), child→parent propagation, indexation/recherche des compaction summaries, fix opérateur OR, modes de recherche (`--any` CSV / `--phrase` / `--include-parents`).
- EXCLUDE: vector DB / embeddings externes / service LLM (contrainte in-app, no external deps).
