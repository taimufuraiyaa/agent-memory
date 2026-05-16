# Human Dashboard (React) - Requirements
## 1. Problem Statement
Engineers and agents can query `agent-memory`, but the current dashboard UI is JSON-centric and does not render memory content the way humans consume it (Markdown, diagrams). Humans need a fast, safe, local-only web UI to query the memory system in natural language (English-only) and visually inspect matched memories, including diagram payloads (Mermaid) with both raw text and rendered views.

## 2. Goals
- Provide a human-friendly search and recall experience over the existing `agent-memory` HTTP APIs.
- Render memory content as Markdown (sanitized) and support rich diagram viewing:
  - Show raw diagram text/code
  - Render Mermaid diagrams inline
- Present results in a table/list that surfaces the most useful metadata (type, tier, confidence, timestamps) and includes diagrams when present.
- Keep the dashboard local-first and bundled into the Go binary (no separate server requirement to use it).

## 3. Non-Goals
- Changing retrieval ranking logic or token budget logic.
- Editing/writing memories from the UI (read-only inspection surface for this iteration).
- Supporting non-English UI query understanding (inputs remain free-form text, but the UI is optimized for English).
- Rendering non-Mermaid diagram languages (PlantUML/DOT) beyond raw code display (rendering may be added later).

## 4. Users and Use Cases
### 4.1 Personas
- **Engineer**: searches memories to validate what an agent would see; inspects Markdown and diagrams; copies raw content.

### 4.2 Core Use Cases
- Search by natural-language query and inspect ranked hits.
- Filter by workspace, type, tier, and optional explain fields.
- Preview recall output for a task and inspect which memories were included/clipped.
- View any Mermaid diagram payloads as both raw code and rendered diagram.

## 5. Functional Requirements
- **FR-UI-1**: Dashboard provides an English-first query input for memory search.
- **FR-UI-2**: Search results are shown as a table/list; each row includes memory metadata and rendered content.
- **FR-UI-3**: If a memory includes `diagram.lang` + `diagram.code`, the UI must display diagram details in the result view.
- **FR-UI-4**: Mermaid diagrams must be renderable in the UI, and the raw Mermaid source must be viewable/copyable.
- **FR-UI-5**: Memory `content` must be rendered as Markdown, with safe HTML sanitization.
- **FR-UI-6**: Recall preview must show the assembled recall text block and a table/list of included memories; if diagram payloads exist, they must be visible.

## 6. Non-Functional Requirements
- **NFR-SEC-1**: The UI must sanitize rendered Markdown to prevent XSS.
- **NFR-SEC-2**: Dashboard remains local-only by default (served by `agent-memory serve`), with no auth in V1; do not add network calls beyond configured base URL.
- **NFR-PERF-1**: Rendering must remain responsive for typical result sizes (Top K up to 200).
- **NFR-REL-1**: UI must degrade gracefully when Mermaid rendering fails (still show raw diagram code).

## 7. Acceptance Criteria
- Search results render Markdown and show diagram payloads when present.
- Mermaid diagrams can be toggled between raw view and rendered view.
- Recall preview shows the recall block plus a list/table of included memories with their Markdown and any diagrams.
- Dashboard is built with React + TypeScript and served as static assets embedded in the Go binary.
