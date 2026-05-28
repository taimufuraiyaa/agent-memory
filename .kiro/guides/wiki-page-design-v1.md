# Wiki Page Design V1

## Goal

Design the next agent-memory wiki surface as a homepage-first knowledge browser.
The user enters through a quiet landing page, opens an expandable search bar at
the bottom, scopes the query to one project or all projects, adjusts relevance
controls, and receives a continuous article-like result instead of a stack of
isolated memory cards.

This design follows the current dashboard constraints:

- ASCII-forward visual language
- semantic similarity exposed honestly
- project-aware search
- inline Mermaid and diagram rendering
- memory pinning retained as a first-class behavior

## Product Direction

Think of the system as a "living wiki compiled from memory fragments."

Instead of:

- search query -> list of memory cards

We move to:

- search query -> one stitched reading surface

The stitched surface should read top to bottom:

- strongest memory evidence first
- weaker but still useful context later
- diagrams rendered inline at the point they help
- pinned memories floated above normal ranking until unpinned

## Homepage Sketch

```text
+----------------------------------------------------------------------------------+
|                                                                                  |
|                           .----------------------------.                         |
|                           |  knowledge atlas           |                         |
|                           |  memory becomes wiki       |                         |
|                           '----------------------------'                         |
|                                                                                  |
|              Browse what the system has learned across time, sessions,           |
|              projects, outcomes, and diagrams.                                   |
|                                                                                  |
|              [pinned threads]  [recent research]  [diagrams]                     |
|              [project maps]    [failures]         [procedures]                   |
|                                                                                  |
|                                                                                  |
|                                                                                  |
|                                                                                  |
|                                                                                  |
|     collapsed dock                                                               |
|     +------------------------------------------------------------------------+   |
|     | search the wiki...                           [all projects v] [go]     |   |
|     | [options v]                                                           |   |
|     +------------------------------------------------------------------------+   |
+----------------------------------------------------------------------------------+
```

## Homepage Expanded Search Sketch

```text
+----------------------------------------------------------------------------------+
|                                                                                  |
|                           .----------------------------.                         |
|                           |  knowledge atlas           |                         |
|                           |  memory becomes wiki       |                         |
|                           '----------------------------'                         |
|                                                                                  |
|                                                                                  |
|  expanded dock                                                                   |
|  +------------------------------------------------------------------------------+|
|  | what do we know about redis search fallback and dashboard ranking?           ||
|  | [all projects v] [wiki article v] [options v]                   [search]     ||
|  |------------------------------------------------------------------------------||
|  | semantic score  0.30 ----|====o----- 0.55                                    ||
|  | total score     any  ----|--o--------- 1.00                                  ||
|  | confidence      any  ----|---o-------- 1.00                                  ||
|  | outcome         [any v]                                                      ||
|  | types           [semantic] [procedural] [episodic] [outcome]                ||
|  | tiers           [vector] [markdown] [vector+graph] [document]               ||
|  |------------------------------------------------------------------------------||
|  | [simple mode] [collapse -]                                   [search wiki]   ||
|  +------------------------------------------------------------------------------+|
+----------------------------------------------------------------------------------+
```

## Result Page Sketch

```text
+----------------------------------------------------------------------------------+
| +------------------------------------------------------------------------------+ |
| | redis search fallback and dashboard ranking                                  | |
| | [all projects v] [wiki article v] [options v]                   [search]     | |
| +------------------------------------------------------------------------------+ |
|                                                                                  |
| [pin rail]                                                                       |
| +------------------------------------------------------------------------------+ |
| | pinned memory: redis retrieval policy upgrade                                 | |
| | why pinned: repeatedly useful to ranking work                                 | |
| | [unpin] [jump to source]                                                      | |
| +------------------------------------------------------------------------------+ |
|                                                                                  |
|                                                   [2 selected] [consolidate v]   |
|                                                                                  |
| # Redis Search Fallback And Dashboard Ranking                                    |
|                                                                                  |
| Most relevant material suggests the ranking issue came from weak semantic gates, |
| not from blended score tuning alone. The dashboard upgrade made semantic         |
| similarity the primary visible signal and exposed the backend floor directly.    |
|                                                                                  |
| [x] [memory: semantic | project: core | relevance: high 0.61 | pin]             |
| The repair playbook says semantic similarity must gate visibility and that the   |
| dashboard should expose `min_semantic_score` honestly.                           |
|                                                                                  |
| +---------------------------------- diagram -----------------------------------+ |
| | flowchart LR                                                                  | |
| | Query --> SemanticGate --> StrongResults --> WikiAssembler                    | |
| |                     \-> WeakResults --> Appendix                              | |
| +------------------------------------------------------------------------------+ |
|                                                                                  |
| [x] [memory: procedural | project: dashboard | relevance: medium 0.44 | pin]    |
| Search controls should mirror backend defaults so operators can diagnose weak    |
| matches without confusing end users.                                             |
|                                                                                  |
| [ ] [memory: episodic | project: dashboard | relevance: low 0.33 | pin]         |
| In the session where the UI changed, semantic similarity was promoted above the  |
| blended score and weak familiarity was visually separated.                       |
|                                                                                  |
| ------------------------------------------------------------------------------   |
| [weak] [tail] [lower confidence]                                                 |
|                                                                                  |
| [ ] [memory: outcome | relevance: weak 0.24 | pin]                              |
| Older notes mention score tuning experiments that matter only if semantic gates  |
| are already correct.                                                             |
|                                                                                  |
| [show boundaries] [hide weak tail] [export article]                              |
+----------------------------------------------------------------------------------+
```

## Structure

### 1. Homepage

- Large quiet hero centered in the page
- No separate top bar
- Search dock anchored to bottom edge
- Default state is collapsed and low-noise
- Expansion grows upward from the bottom, not downward from the hero

### 2. Search Dock

- One main text field
- Project scope lives inside the search bar as a dropdown
- Additional controls live behind a simple `options` dropdown in the same bar
- Uses current dashboard search controls as the source of truth for score tuning
- Default scope is `all projects`

### 3. Result Surface

- Single reading column
- Results stitched into a long article
- Memory boundaries remain visible but soft
- Ranking fades from strongest at top to weaker context near bottom
- Weak familiarity and suppressed material stay optional, not mixed into the main flow by default
- Each memory fragment supports selection with a minimal checkbox-style marker like `[ ]` and `[x]`
- User can select one or more memory fragments directly from the article stream

### 4. Selection And Consolidation

- When at least one memory is selected, show a small top-right badge control
- Default compact state is `[2 selected] [consolidate v]`
- Keep it badge-like and quiet, not a panel or toolbar
- Opening the dropdown reveals `open`, `download`, and `print`
- Consolidated view merges only the selected memory fragments into one clean reading/export surface
- Consolidated view keeps inline diagrams when present
- Download should export the consolidated view, not the entire result page
- Print should print the consolidated view with simplified chrome

### 5. Pinning

- A memory can be pinned from any stitched segment
- Pinned memories appear in a top rail before the generated article body
- Pinned memories remain surfaced over time until explicitly unpinned
- Pin state should also subtly affect future stitched assembly order

### 6. Diagrams

- If a memory contains Mermaid or structured diagram payloads, render inline inside the article
- Diagrams should appear where the memory is stitched, not only in a side drawer
- A larger lightbox or fullscreen mode can remain available as a secondary action

## Motion Spec

### Bottom Search Bar

Collapsed:

- shows one-line input
- shows scope dropdown
- shows search action

Expanded:

- animates upward with height + opacity + slight translate
- query area grows first
- advanced controls fade in second
- action row settles last

Suggested timing:

- dock container: `240ms ease`
- advanced filter body: `180ms ease` with `40ms` delay
- arrow / plus-minus affordance: `160ms ease`

The motion should feel like "unfolding a drawer from the bottom edge."

## Content Assembly Rules

The stitched article should not invent prose that hides the sources. It should
feel assembled, not hallucinated.

Recommended assembly model:

1. group search results by descending relevance band
2. move pinned memories to a dedicated top rail
3. keep each memory as a readable section fragment
4. insert small connective headings between clusters
5. place diagrams inside the fragment where they belong
6. move low-confidence or weak results into a closing weak-tail section

Each fragment can show:

- type
- workspace / project
- semantic similarity
- pin action
- source jump

## Information Hierarchy

Primary:

- query
- project scope
- stitched wiki article

Secondary:

- semantic threshold and related score controls
- per-memory provenance
- pin and source actions

Tertiary:

- raw boundaries
- weak familiarity tail
- suppressed memory drawer

## Why This Fits The Current System

- The current dashboard already exposes semantic score controls and relevance pills
- `MemoryEntry` already includes `workspace`, `pinned`, `diagram`, `type`, `score`, and `score_breakdown`
- Mermaid rendering already exists and can be reused inline
- The new design changes presentation more than the underlying retrieval mechanics

## Suggested Next Build Order

1. build homepage hero + bottom dock in ASCII style
2. move current search controls into expandable dock sections
3. add `all projects` scope handling in the UI and API integration path
4. replace result-card list rendering with stitched article rendering
5. add inline diagram blocks inside stitched memories
6. add pin / unpin actions directly in the stitched article
7. add optional raw-memory boundary view for debugging
