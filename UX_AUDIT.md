# UX Audit — aisync Dashboard

## Audit de chaque page : objectif, problemes, ameliorations

---

### 1. Dashboard Home (`/`)

**Objectif** : Vue d'ensemble rapide — "comment vont mes projets AI ?"

**Ce qui existe** :
- KPIs : sessions, tokens, cost, tool calls, errors
- Weekly trends (sessions/tokens/errors with delta)
- Top branches table
- Recent sessions table
- Forecast (projected 30/90 days)

**Problemes** :
- [ ] Le cout affiche est confus ($9k "subscription" — c'est quoi exactement ?)
- [ ] Les KPIs sont tous au meme niveau — pas de hierarchie visuelle
- [ ] "Top Branches" montre des branches vides/unknown — pas utile
- [ ] "Recent Sessions" montre seulement ID + branch + tokens — pas d'intent/summary lisible
- [ ] Pas de graphique temporel (sparkline/trend chart)
- [ ] Pas d'indication des sessions actives vs archivees

**Ameliorations proposees** :
1. **KPI principal** : "Sessions actives" en gros, avec les autres en secondaire
2. **Sparkline tokens/jour** : petit graphique inline sur les 7 derniers jours
3. **Cost card** : afficher "Equivalent API: $X" en petit + "Subscription: $Y/mois" clairement
4. **Recent sessions** : afficher le summary/intent en priorite, pas juste l'ID
5. **Alertes** : section "Attention" si error rate > 10% ou sessions bloquees
6. **Quick actions** : boutons "Capture now", "Run analysis"

---

### 2. Sessions List (`/sessions`)

**Objectif** : Trouver et filtrer les sessions — "quelle session je cherche ?"

**Ce qui existe** :
- Filtres : keyword, branch, provider, owner, session type, project category, since/until
- Table paginee : ID, provider, summary, messages, tokens, captured

**Problemes** :
- [ ] L'ID est l'element le plus visible — personne ne se souvient des IDs
- [ ] Le summary est tronque et pas lisible
- [ ] Pas de badge status (active/idle/archived)
- [ ] Pas de badge session type (feature/bug)
- [ ] Pas de preview au hover
- [ ] Les filtres prennent beaucoup de place
- [ ] Pas de tri par colonne (sauf sort_by param)

**Ameliorations proposees** :
1. **Card layout** au lieu de table — chaque session = une carte avec summary en gros, badges, metrics en petit
2. **Status badge** : vert (active), jaune (idle), gris (archived)
3. **Type badge** : feature, bug, refactor, etc.
4. **Hover preview** : apercu des premiers messages
5. **Filtres collapsibles** : barre de recherche prominente, filtres avances en dropdown
6. **Tri interactif** : cliquer sur les headers pour trier

---

### 3. Session Detail (`/sessions/{id}`)

**Objectif** : Comprendre ce qu'une session a fait — "qu'est-ce qui s'est passe ici ?"

**Ce qui existe** :
- Metadata (provider, agent, branch, created, mode)
- KPIs (messages, tokens, cost, tool calls, errors)
- Work Objective (intent/outcome)
- Analysis section
- Tool usage table
- Cost breakdown
- Activity bar
- Fork tree
- Conversation (paginated, filtered)

**Problemes** :
- [ ] Tool usage table prend trop de place (16 lignes visibles)
- [ ] La conversation est le contenu principal mais c'est tout en bas
- [ ] Les KPIs ne sont pas assez visuels (juste des chiffres)
- [ ] L'activity bar est trop petite pour etre utile
- [ ] Pas de navigation rapide dans la conversation (jump to error, jump to user msg)
- [ ] Pas de resume de ce qui a ete fait (files changed, commits)

**Ameliorations proposees** :
1. **Layout en 2 colonnes** : sidebar gauche (metadata + KPIs + tools compact) + main droite (conversation)
2. **Tool usage compact** : top 5 en barres horizontales, le reste en `<details>`
3. **Quick jump** : boutons "Jump to error #1", "Jump to user message #3"
4. **Files changed** : section montrant les fichiers touches avec diff stats
5. **Activity bar** : plus grande, cliquable (clic = jump au message)
6. **Sticky header** : metadata reste visible quand on scroll

---

### 4. Projects (`/projects`)

**Objectif** : Vue par projet — "quel projet consomme quoi ?"

**Ce qui existe** :
- Grid de cartes : nom, remote URL, provider, category, sessions count, tokens

**Problemes** :
- [ ] Pas de graphique de tendance par projet
- [ ] Pas de derniere activite (quand est la derniere session ?)
- [ ] Pas de comparaison entre projets
- [ ] Les cartes sont toutes identiques visuellement

**Ameliorations proposees** :
1. **Derniere activite** : "Last session: 2h ago" sur chaque carte
2. **Mini sparkline** : tokens/semaine sur chaque carte
3. **Tri** : par sessions, tokens, derniere activite
4. **Comparaison** : barre horizontale montrant la repartition tokens entre projets

---

### 5. Branches (`/branches`)

**Objectif** : Explorer le travail par branche — "qu'est-ce qu'on a fait sur cette branche ?"

**Ce qui existe** :
- Cartes par branche avec timeline de sessions
- Lien vers la timeline interleaved

**Problemes** :
- [ ] Les branches vides/unknown polluent la vue
- [ ] La timeline est basique (juste ID + summary tronque)
- [ ] Pas d'indication du travail accompli (objectives)
- [ ] Pas de lien vers les commits

**Ameliorations proposees** :
1. **Cacher les branches unknown/vides** par defaut
2. **Objectifs inline** : intent/outcome sous chaque session dans la timeline
3. **Commits inline** (via la branch timeline)
4. **Filtrer** : "active branches only"

---

### 6. Costs (`/costs`)

**Objectif** : Comprendre les couts — "combien je depense et ou ?"

**Ce qui existe** :
- KPIs : total cost, actual cost, savings
- Per-project breakdown
- Per-model breakdown
- Per-branch breakdown

**Problemes** :
- [ ] Le cout theorique vs reel n'est pas clair
- [ ] Pas de graphique temporel (cout par jour/semaine)
- [ ] Pas de tendance (augmente ou diminue ?)
- [ ] Pas de recommandation ("si tu passes a Sonnet tu economises X")

**Ameliorations proposees** :
1. **Graphique cout/jour** : bar chart des 30 derniers jours
2. **Tendance** : fleche + pourcentage (ce mois vs le precedent)
3. **Explication** : "You're on a subscription. API equivalent would be $X"
4. **Recommendation** : "Your most expensive model is Opus. Sonnet would cost Y% less"

---

### 7. Usage (`/usage`)

**Objectif** : Analyser les patterns d'utilisation — "quand est-ce que je consomme le plus ?"

**Ce qui existe** :
- Heatmap 7j x 24h
- Bar chart horaire moyen
- KPIs : peak hour, night/day/evening breakdown
- Activity breakdown table

**Problemes** :
- [ ] Le toggle heatmap/barchart est petit
- [ ] Pas de filtre par projet
- [ ] Pas de comparaison semaine vs semaine precedente
- [ ] Le breakdown table n'a pas de visualisation

**Ameliorations proposees** :
1. **Filtre projet** : dropdown pour voir l'usage d'un projet specifique
2. **Comparaison** : overlay semaine precedente en transparence sur la heatmap
3. **Mini barres** : dans le breakdown table, ajouter des barres proportionnelles
4. **Human ratio gauge** : indicateur visuel jour (humain) vs nuit (agent)

---

## Priorites d'implementation

### P0 — Impact immediat (UX critique)
1. Session list : card layout + badges status/type
2. Session detail : layout 2 colonnes + tools compact
3. Dashboard : KPI hierarchie + recent sessions avec summary

### P1 — Qualite de vie
4. Projects : derniere activite + tri
5. Branches : cacher unknown + objectives inline
6. Costs : graphique temporel

### P2 — Polish
7. Usage : filtre projet + comparaison
8. Dashboard : sparklines + alertes
9. Session detail : quick jump + files changed
