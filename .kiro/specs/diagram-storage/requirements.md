# Diagram Storage - Requirements

## Goal

Store investigation diagrams (Mermaid flowcharts/sequence diagrams and similar) in the memory store in a structured way, so agents and humans can recall the raw diagram code without losing formatting or contaminating the primary text content.

## Functional Requirements

- Persist diagram content as a first-class field on a memory entry (not embedded in the `content` text).
- Support at least:
  - Mermaid (`mermaid`)
  - PlantUML (`plantuml`)
  - DOT / Graphviz (`dot`, `graphviz`)
- When a write includes a fenced diagram block, extract it and store:
  - Diagram language
  - Diagram raw code
- Recall output includes the diagram as a fenced block so the reader can render/copy it easily.
- Session-end extraction preserves diagrams as a single memory item (not split per line).

## Non-Functional Requirements

- Backwards compatible with existing databases (idempotent migration).
- Security filter applies to diagram code (secrets/PII/poisoning protections remain enforced).
- Token budgeting counts diagram text so recall does not exceed configured budgets.

## Out of Scope (for this iteration)

- Multiple diagrams per memory entry.
- Rendering diagrams in the dashboard UI (storage + recall text only).
