# Slack Integration — Feature Spec & Architecture

> Status: Draft — 2026-04-01

## Vue d'ensemble

Integration Slack pour aisync : alertes temps reel, digests quotidiens/hebdo, DMs personnalises par developpeur, gestion des comptes machines (bots/CI).

---

## Problemes resolus

1. **Pas de visibilite passive** — aujourd'hui il faut ouvrir le dashboard pour voir les couts/erreurs
2. **Pas de responsabilite individuelle** — on voit les sessions par projet mais pas qui consomme quoi
3. **Comptes machines non distingues** — un Claude reviewer GitHub genere des sessions au meme titre qu'un humain
4. **Pas de recap quotidien** — les developpeurs ne savent pas combien ils ont consomme dans la journee

---

## Analyse de l'existant

### Deux systemes d'utilisateurs separes (probleme)

Actuellement il y a **deux tables users completement deconnectees** :

| Table | Package | But | Champs |
|-------|---------|-----|--------|
| `users` | `session.User` | Git identity auto-creee | id, name, email, source, created_at |
| `auth_users` | `auth.User` | Credentials + RBAC | id, username, password_hash, role, active, created_at |

**Problemes :**
- Aucun lien entre les deux (pas de FK, pas de mapping, pas d'ID partage)
- `owner_id` sur sessions reference `users.id`, PAS `auth_users.id`
- La table `users` n'a **pas de ListUsers()**, pas de CLI, pas d'API
- La colonne `owner_id` n'a **pas d'index** sur `sessions` (full-scan sur 1300+ sessions)
- `resolveOwner()` est best-effort — si git est absent, owner_id est vide
- Pas de concept human/machine sur `users`

### Webhook dispatcher existant

Le `internal/webhooks/Dispatcher` est deja fonctionnel :
- Fire-and-forget avec goroutines
- Retry lineaire (1 retry par defaut)
- 5 event types : `session.captured`, `session.analyzed`, `session.tagged`, `skill.missed`, `budget.alert`
- Config via `webhooks.hooks[]` dans config.json
- Seul `BudgetCheckTask` utilise le dispatcher dans le scheduler
- `PostCaptureFunc` fire 3 events (captured, tagged, analyzed)

### Sources de donnees disponibles pour les digests

| Besoin | Source | Pret ? |
|--------|--------|--------|
| Cout par session | `pricing.SessionCost(sess)` | Oui |
| Cout par projet/jour | `store.QueryTokenBuckets("1d", since, until, project)` | Oui |
| Budget status | `service.BudgetStatus()` — spent, limit, projected | Oui |
| Sessions par jour | `TokenUsageBucket.SessionCount` | Oui |
| Erreurs par jour | `TokenUsageBucket.ToolErrorCount` | Oui |
| Tendances semaine | `service.Trends(TrendRequest)` — delta sessions/tokens/errors | Oui |
| Liste projets | `store.ListProjects()` — GROUP BY remote_url | Oui |
| Stats par owner | **MANQUANT** — pas de GROUP BY owner_id | **A creer** |
| Liste des users | **MANQUANT** — pas de `ListUsers()` | **A creer** |
| Classification human/machine | **MANQUANT** — pas de champ `kind` | **A creer** |

---

## Fonctionnalites

### Tier 1 — Alertes temps reel (v1)

| Alerte | Trigger | Destinataire | Contenu |
|--------|---------|-------------|---------|
| **Budget mensuel** | Projet a X% du budget | Channel projet | Progress bar, spent/limit, projection fin de mois |
| **Budget quotidien** | Depassement budget jour | Channel projet | Spent vs limit, top sessions du jour |
| **Spike d'erreurs** | >N erreurs en 10 min pour un projet | Channel projet | Nombre d'erreurs, sessions concernees, types d'erreurs |
| **Session critique** | Session >Y tokens en zone critique (>80% contexte) | DM a l'owner | Tokens consommes, % contexte, recommandation split |
| **Capture summary** | Nouvelle session capturee (opt-in) | Channel projet | Session ID, agent, branch, resume, tokens |

### Tier 2 — Digests personnalises (v2)

| Digest | Frequence | Destinataire | Contenu |
|--------|-----------|-------------|---------|
| **Daily personal** | 18h (configurable) | DM a chaque owner humain | Ses sessions du jour, cout total, erreurs, top branches |
| **Daily project** | 18h | Channel projet | Resume du projet : sessions, cout, erreurs, contributors |
| **Weekly report** | Lundi 9h | Channel projet | Tendances semaine, cout vs budget, PRs mergees, recommendations, leaderboard |
| **Weekly personal** | Lundi 9h | DM a chaque owner humain | Son resume hebdo : cout, sessions, erreurs, skills utilises |

### Tier 3 — Interactif (v3, futur)

| Feature | Description |
|---------|-------------|
| **Slash command** | `/aisync status [project]` — etat rapide (cout du jour, sessions actives) |
| **Action buttons** | "Voir session" -> lien dashboard, "Detail" -> thread avec infos |
| **Thread grouping** | Sessions longues (>30 min) : notifications groupees dans un thread |

---

## Owner Identity & Machine Accounts

### Le probleme

L'`OwnerID` actuel est resolu via `git config user.email`. Ca fonctionne pour les humains, mais :
- Un bot GitHub (ex: `claude-reviewer[bot]@users.noreply.github.com`) genere aussi des sessions
- Un CI runner (`ci@company.com`) aussi
- On ne veut pas envoyer un DM Slack a un bot
- On veut que les sessions machine soient attribuees a l'admin du projet

### Solution : Enrichir la table `users`

**Migration SQL :**

```sql
-- Nouveaux champs sur users
ALTER TABLE users ADD COLUMN kind TEXT NOT NULL DEFAULT 'human';
  -- 'human' | 'machine' | 'unknown'

ALTER TABLE users ADD COLUMN slack_id TEXT;
  -- Slack user ID pour DMs (ex: 'U0123ABCDEF')

ALTER TABLE users ADD COLUMN slack_name TEXT;
  -- Slack display name pour mentions

ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'member';
  -- 'admin' | 'member' — pour le routing des notifs machine

-- Index manquant sur sessions.owner_id (perf critique pour GROUP BY)
CREATE INDEX IF NOT EXISTS idx_sessions_owner_id ON sessions(owner_id);
```

**Classification automatique des comptes :**

| Pattern email | Kind | Exemples |
|---------|------|---------|
| `*[bot]@*` | machine | `claude-reviewer[bot]@users.noreply.github.com` |
| `ci@*`, `bot@*`, `automation@*` | machine | `ci@company.com`, `bot@deploy.io` |
| `dependabot*`, `renovate*`, `github-actions*` | machine | `dependabot[bot]@github.com` |
| `*noreply*` (sans `[bot]`) | unknown | `12345+user@users.noreply.github.com` |
| Tout le reste | human | `john@company.com` |

La classification se fait dans `resolveOwner()` au moment de la creation du user — on ajoute un appel a `classifyUserKind(email)`.

### Nouveaux methodes store necessaires

```go
// Sur UserStore (internal/storage/store.go)
type UserStore interface {
    SaveUser(user *session.User) error            // existant
    GetUser(id session.ID) (*session.User, error)  // existant
    GetUserByEmail(email string) (*session.User, error)  // existant

    // NOUVEAUX :
    ListUsers() ([]*session.User, error)                    // tous les users
    ListUsersByKind(kind string) ([]*session.User, error)   // filtrer par kind
    UpdateUserSlack(id session.ID, slackID, slackName string) error
    UpdateUserKind(id session.ID, kind string) error
    UpdateUserRole(id session.ID, role string) error
}
```

### Nouvelle query store : stats par owner

```go
// OwnerStats retourne sessions/tokens/erreurs groupes par owner_id
// pour une periode donnee et optionnellement un projet
type OwnerStat struct {
    OwnerID      session.ID
    OwnerName    string
    OwnerEmail   string
    OwnerKind    string  // human, machine, unknown
    SessionCount int
    TotalTokens  int
    ErrorCount   int
}

func (s *Store) OwnerStats(projectPath string, since, until time.Time) ([]OwnerStat, error)
```

SQL sous-jacent :
```sql
SELECT
    s.owner_id,
    COALESCE(u.name, '') as owner_name,
    COALESCE(u.email, '') as owner_email,
    COALESCE(u.kind, 'unknown') as owner_kind,
    COUNT(*) as session_count,
    COALESCE(SUM(s.total_tokens), 0) as total_tokens,
    COALESCE(SUM(s.error_count), 0) as error_count
FROM sessions s
LEFT JOIN users u ON s.owner_id = u.id
WHERE s.created_at >= ? AND s.created_at <= ?
  AND (? = '' OR s.project_path = ?)
GROUP BY s.owner_id
ORDER BY session_count DESC
```

### Routing des notifications

```
Session capturee / Alerte generee
  |
  v
Owner de la session = ?
  |
  +-- human + slack_id connu  --> DM a l'owner
  |
  +-- machine                 --> DM aux admins du projet (config slack.projects.X.admins)
  |
  +-- unknown ou pas de slack --> Channel projet uniquement
  |
  +-- pas de channel projet   --> Channel par defaut (slack.default_channel)
```

---

## Architecture

### Vue packages

```
internal/
|-- session/
|   +-- session.go           # User struct enrichi (kind, slack_id, slack_name, role)
|
|-- storage/
|   +-- store.go             # UserStore enrichi (ListUsers, UpdateUserSlack, etc.)
|   +-- sqlite/sqlite.go     # Migration + implementations
|
|-- service/
|   +-- owner.go             # NOUVEAU — OwnerService
|   +-- owner_test.go        #   classifyUserKind(), OwnerStats(), resolveSlackRecipient()
|
|-- webhooks/
|   +-- dispatcher.go        # Existant — enrichi avec routeToSlack()
|   +-- slack/               # NOUVEAU
|       +-- client.go        #   SlackClient (webhook mode + bot mode)
|       +-- client_test.go
|       +-- formatter.go     #   Block Kit message builders
|       +-- formatter_test.go
|       +-- digest.go        #   CollectDailyDigest(), CollectWeeklyReport()
|       +-- digest_test.go
|
|-- scheduler/
|   +-- slack_daily_task.go  # NOUVEAU — DailyDigestTask
|   +-- slack_weekly_task.go # NOUVEAU — WeeklyReportTask
|
|-- config/
    +-- config.go            # Section slack.* + owners.*
```

### Slack Client — Architecture deux modes

```
SlackClient
|
+-- Mode Webhook (v1)
|   - Config: slack.webhook_url
|   - HTTP POST vers Incoming Webhook URL
|   - Envoie dans un seul channel (le default ou override)
|   - Pas de DMs possibles
|   - Pas de lookup email->user
|   - Zero dependance externe (net/http direct)
|
+-- Mode Bot App (v2)
    - Config: slack.bot_token (xoxb-...)
    - Slack Web API via HTTP direct (pas de lib externe)
    - chat.postMessage -> n'importe quel channel ou DM
    - users.lookupByEmail -> auto-resolve slack_id depuis email git
    - conversations.open -> ouvrir un DM
    - Scopes necessaires: chat:write, users:read.email
```

**Decision : pas de lib externe.** On utilise `net/http` direct comme pour l'adapter Elasticsearch. Le Block Kit JSON est construit par nos formatters.

### Dispatcher enrichi — Flow

```
Event (budget.alert, session.captured, etc.)
  |
  v
Dispatcher.Fire(eventType, data)
  |
  +-- Pour chaque hook configure :
  |   |
  |   +-- hook.URL commence par "https://hooks.slack.com/" ?
  |   |   OUI -> routeSlack(hook, event)
  |   |          - Formatte en Block Kit via formatter
  |   |          - Determine le channel (projet override ou default)
  |   |          - POST vers webhook URL
  |   |
  |   +-- NON -> HTTP POST generique (comportement actuel)
  |
  +-- Si slack.bot_token configure :
      +-- DM routing via OwnerService.resolveSlackRecipient()
```

### Config

```jsonc
{
  "slack": {
    // Mode 1 : Incoming Webhook (simple, pas de DMs)
    "webhook_url": "https://hooks.slack.com/services/T.../B.../xxx",

    // Mode 2 : Slack App (DMs, lookup email->user, channels multiples)
    "bot_token": "",           // xoxb-... (optionnel, pour DMs + slash commands)

    "default_channel": "#ai-sessions",
    "dashboard_url": "http://localhost:8371",  // pour les liens "View in dashboard"

    "alerts": {
      "budget": true,          // alertes budget (defaut: true si webhook configure)
      "errors": true,          // alertes spike d'erreurs
      "error_threshold": 5,    // nombre d'erreurs pour trigger une alerte
      "error_window_minutes": 10,
      "saturation": false,     // alertes sessions en zone critique
      "capture": false         // notification a chaque capture (bruyant)
    },

    "digest": {
      "daily": true,
      "daily_cron": "0 18 * * 1-5",  // 18h en semaine
      "weekly": true,
      "weekly_cron": "0 9 * * 1",    // lundi 9h
      "personal": true,        // DMs personnalises (necessite bot_token)
      "timezone": "Europe/Paris"
    },

    // Overrides par projet
    "projects": {
      "my-org/backend": {
        "channel": "#backend-ai",
        "admins": ["john@company.com", "jane@company.com"]
      }
    }
  },

  "owners": {
    "machine_patterns": [
      "*[bot]@*",
      "ci@*",
      "dependabot*",
      "renovate*",
      "github-actions*"
    ]
  }
}
```

### Digest Data Collection

Les digests s'appuient sur les sources existantes — **pas de nouvelles queries lourdes** :

```
DailyDigestTask.Run()
  |
  +-- store.QueryTokenBuckets("1d", today, now, "")
  |   -> sessions, tokens, cost, errors par projet pour aujourd'hui
  |
  +-- store.OwnerStats("", today, now)           [NOUVEAU]
  |   -> GROUP BY owner_id pour le breakdown contributors
  |
  +-- service.BudgetStatus()
  |   -> spent vs limit pour chaque projet avec budget
  |
  +-- Formatter -> Block Kit JSON
  |
  +-- SlackClient.PostMessage(channel, blocks)
  |
  +-- Si personal=true && bot_token :
      +-- Pour chaque owner humain avec slack_id :
          +-- Filtre ses sessions du jour
          +-- Formatter -> Block Kit DM
          +-- SlackClient.PostDM(slack_id, blocks)
```

```
WeeklyReportTask.Run()
  |
  +-- service.Trends(TrendRequest{Period: 7d})
  |   -> delta sessions/tokens/errors vs semaine precedente
  |
  +-- store.QueryTokenBuckets("1d", weekStart, now, project)
  |   -> courbe quotidienne sur 7 jours
  |
  +-- store.OwnerStats(project, weekStart, now)  [NOUVEAU]
  |   -> leaderboard contributors
  |
  +-- service.BudgetStatus()
  |   -> budget progress
  |
  +-- store.ListPRsWithSessions("", "", "merged", 50)
  |   -> PRs mergees cette semaine (si PR sync active)
  |
  +-- Formatter -> Block Kit JSON
  +-- SlackClient.PostMessage(channel, blocks)
```

### Block Kit — Exemples de messages

**Budget Alert :**
```
:warning: Budget Alert: my-org/backend

*Monthly:* $120 / $150 (80%)
||||||||||||||||....  <- progress bar en emoji

*Projected:* $185 by month end
*Top consumer:* @john (45% of spend)
*Sessions today:* 12

[View Dashboard ->]
```

**Daily Project Digest :**
```
:chart_with_upwards_trend: Daily AI Report — my-org/backend — Apr 1

Sessions: 14  |  Tokens: 380K  |  Cost: $5.20  |  Errors: 3

Contributors:
  @john     8 sessions  $3.10  1 error
  @jane     4 sessions  $1.40  2 errors
  :robot_face: claude-reviewer  2 sessions  $0.70  0 errors (-> @john)

Budget: $45 / $200 (23%) — on track

[View Dashboard ->]
```

**Weekly Report :**
```
:bar_chart: Weekly AI Report — my-org/backend — W14

Sessions: 67 (+12%)  |  Cost: $45.20 (-5%)  |  Errors: 8 (-38%)
Verdict: :white_check_mark: improving

Leaderboard:
  1. @john    28 sessions  $18.50  3 errors
  2. @jane    22 sessions  $14.30  1 error
  3. @alex    12 sessions  $8.40   4 errors :warning:

Machine Accounts (notified to admins):
  :robot_face: claude-reviewer  5 sessions  $4.00  0 errors

PRs Merged: 8 (linked to 34 AI sessions)

Budget: $45.20 / $200.00 (23%)

Recommendations:
  - @alex error rate 4x team avg — review config
  - 3 sessions hit critical context — split tasks

[View Full Report ->]
```

**Personal DM :**
```
:wave: Your AI Sessions Today — Apr 1

Sessions: 8  |  Tokens: 245K  |  Cost: $3.20  |  Errors: 2

Top Branches:
  feature/auth  3 sessions  $1.50
  fix/login     2 sessions  $0.80
  main          3 sessions  $0.90

vs Team Average: +15% cost ($3.20 vs $2.78 avg)

[View Your Sessions ->]
```

---

## Plan d'implementation

### Phase 1 — Foundation (prealables)

**But :** Poser les bases user identity + Slack client sans rien casser.

1. **Migration `users` table** — ajouter `kind`, `slack_id`, `slack_name`, `role`
2. **Index `sessions.owner_id`** — performance critique pour GROUP BY
3. **`User` struct enrichi** — nouveaux champs dans `session.User`
4. **`UserStore` enrichi** — `ListUsers()`, `UpdateUserSlack()`, `UpdateUserKind()`, `UpdateUserRole()`
5. **`classifyUserKind(email)`** — pure function, patterns configurables
6. **`resolveOwner()` enrichi** — appelle `classifyUserKind()` a la creation du user
7. **`OwnerStats()` query** — GROUP BY owner_id avec LEFT JOIN users
8. **Backfill task** — classifier les users existants (kind basee sur email)
9. **CLI `aisync users list`** — voir tous les users avec kind/role
10. **CLI `aisync users set-kind/set-slack/set-role`** — admin manual overrides
11. **Tests** — migration, classification patterns, owner stats query, CLI

### Phase 2 — Slack Alerts

**But :** Envoyer les premieres alertes en channel.

1. **Config `slack.*`** — webhook_url, alerts toggles, default_channel
2. **`slack/client.go`** — webhook mode, POST Block Kit JSON
3. **`slack/formatter.go`** — budget alert, error spike, capture summary
4. **Wire dans le dispatcher** — detecter Slack URLs, formater en Block Kit
5. **`BudgetCheckTask` enrichi** — utilise le Slack formatter au lieu du generic webhook
6. **Error spike detection** — nouveau event type + logique de dedup (1 alerte/30min/projet)
7. **Tests** — client mock HTTP, formatter unit tests, dedup logic

### Phase 3 — Digests

**But :** Recap quotidien/hebdo en channel.

1. **`slack/digest.go`** — `CollectDailyDigest()`, `CollectWeeklyReport()`
2. **`DailyDigestTask`** — scheduler task, collecte + format + envoi
3. **`WeeklyReportTask`** — scheduler task, trends + leaderboard + budget
4. **Config `slack.digest.*`** — cron, timezone
5. **Config `slack.projects.*`** — channel overrides, admins par projet
6. **Machine account routing** — sessions machine attribuees aux admins dans le digest
7. **Tests** — digest collection, formatting, scheduler tasks

### Phase 4 — DMs personnalises

**But :** Recaps individuels par developpeur.

1. **Bot token support** — `slack/client.go` mode Web API
2. **`users.lookupByEmail`** — auto-resolve slack_id depuis email git
3. **Personal daily digest** — filtre sessions de l'owner, compare vs team avg
4. **Personal weekly digest** — resume hebdo individuel
5. **DM routing pour machines** — sessions bot -> DM aux admins
6. **Tests** — bot API mock, lookup, DM routing logic

---

## Decisions de design

| Decision | Choix | Raison |
|----------|-------|--------|
| **Client Slack** | HTTP direct, pas de lib | Coherence avec ES adapter, zero dependance |
| **Detection machine** | Pattern matching sur email | Simple, configurable, couvre 95% des cas |
| **Stockage digests** | Fire-and-forget (pas de table) | Simplicity — le dashboard a deja toutes les donnees |
| **Dedup error alerts** | 1 alerte / 30 min / projet | Evite le spam en cas de cascade d'erreurs |
| **Timezone** | Configurable, defaut UTC | Les crons du scheduler sont deja en UTC |
| **Lien users<->auth_users** | Pas de lien pour l'instant | Complexite non justifiee — les cas d'usage sont differents |
| **Admin du projet** | Config manuelle (slack.projects.X.admins) | Pas de detection automatique possible sans GitHub team API |

---

## Risques et mitigations

| Risque | Impact | Mitigation |
|--------|--------|------------|
| owner_id vide sur beaucoup de sessions | Digests incomplets | Backfill task + message "N sessions sans owner" dans le digest |
| Slack webhook rate limit (1 msg/sec) | Alertes perdues en rafale | Queue interne + batching si >5 events en attente |
| GROUP BY owner_id sur 1300+ sessions | Lenteur | Index idx_sessions_owner_id + cache 5min |
| Bot token leak dans config.json | Securite | Supporter env var `AISYNC_SLACK_BOT_TOKEN` en priorite |
| Timezone des crons vs timezone Slack | Digest envoye a mauvaise heure | Config explicite `slack.digest.timezone`, conversion dans le task |
