# Human Dashboard (React) - Tasks

## Master Checklist
- [x] **HD-01** Spec lock (requirements/design/tasks reviewed and boundaries confirmed)
- [x] **HD-02** API support for recall preview table (optional full MemoryEntry payload)
- [x] **HD-03** React + TypeScript dashboard implementation (search + recall preview UI)
- [x] **HD-04** Embed built dashboard assets into Go binary (`internal/api/dashboard/`)
- [x] **HD-05** Validation (Go tests + manual dashboard smoke check)
- [x] **HD-06** Chat-style UI (Claude-like conversation + composer)

---

## HD-01 Spec lock
| Field | Value |
|---|---|
| **Status** | done |
| **Scope** | Confirm this feature is read-only inspection UI; Markdown + Mermaid rendering; uses existing APIs with additive recall-preview enhancement |

Acceptance:
- [x] Requirements reviewed for completeness
- [x] Design reviewed for data contracts + security/perf considerations

## HD-02 API support for recall preview table
| Field | Value |
|---|---|
| **Status** | done |
| **Scope** | Add `include_memories` request flag to recall preview and return `memories_included_full: MemoryEntry[]` when requested |

Acceptance:
- [x] Backward compatible (existing clients unchanged)
- [x] `diagram` fields included when present
- [x] API tests updated/added for the new field behavior

## HD-03 React + TypeScript dashboard implementation
| Field | Value |
|---|---|
| **Status** | done |
| **Scope** | Build a React+TS UI that calls the HTTP APIs, renders Markdown safely, and renders Mermaid diagrams with a raw toggle |

Acceptance:
- [x] Search panel (workspace + query + key filters + explain toggle)
- [x] Results table renders Markdown (sanitized) and shows diagram raw + Mermaid render when `diagram.lang == "mermaid"`
- [x] Recall preview panel shows `context_block` and included memory table (using `include_memories`)
- [x] Graceful fallback when Mermaid render fails

## HD-04 Embed built dashboard assets into Go binary
| Field | Value |
|---|---|
| **Status** | done |
| **Scope** | Replace the embedded dashboard assets under `internal/api/dashboard/` with the built SPA assets; keep served route `/dashboard/` |

Acceptance:
- [x] `agent-memory serve` serves the new dashboard at `/dashboard/`
- [x] No runtime dependency on a Node dev server

## HD-05 Validation
| Field | Value |
|---|---|
| **Status** | done |
| **Scope** | Run focused Go tests and perform a local UI smoke test (search + mermaid render + recall preview) |

Acceptance:
- [x] `go test ./...` passes
- [x] Dashboard loads, search works, Markdown renders, Mermaid renders, recall preview works

## HD-06 Chat-style UI
| Field | Value |
|---|---|
| **Status** | done |
| **Scope** | Redesign the dashboard interaction model into a chat conversation: message thread + bottom composer; advanced options behind an expand toggle |

Acceptance:
- [x] Main UI is a message thread (user prompt → assistant response)
- [x] Composer sits at the bottom; Enter sends, Shift+Enter inserts newline
- [x] Advanced options are hidden behind an `Advanced` toggle
- [x] Search responses render Markdown + diagrams within assistant messages
- [x] Recall preview responses include context block + included memories within assistant messages
