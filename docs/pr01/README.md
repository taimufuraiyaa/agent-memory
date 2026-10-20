# PR01 — BM25-only Retrieval, Embedded Normalizer LLM, tách Jev và mini LLM

Thư mục này là `output_folder` của BMAD-METHOD v6.12.4 (`_bmad/bmm/config.yaml`). Artifact được đặt theo layout chuẩn để các skill `bmad-*` đọc và cập nhật tiếp.

```text
docs/pr01/
  planning-artifacts/
    briefs/brief-agent-memory-2026-10-20/         brief.md, addendum.md, .memlog.md
    prds/prd-agent-memory-2026-10-20/             prd.md, addendum.md, .memlog.md
    architecture/architecture-agent-memory-2026-10-20/
      ARCHITECTURE-SPINE.md                       AD-1..AD-11 (lint_spine: 0 findings)
      solution-design.md                          companion: thiết kế chi tiết, phương án, rủi ro, rollout
      large-text-input.md                         companion: 10 phương án cho văn bản lớn
      diagrams/                                   PlantUML (.puml, png, svg)
      .memlog.md
    epics.md                                      Epic 1–7, story Given/When/Then, FR coverage map
  implementation-artifacts/                       sprint-status, stories, reviews (sinh ở bước sau)
```

| Bước BMAD | Artifact | Skill để tiếp tục |
|---|---|---|
| Brief | [brief.md](planning-artifacts/briefs/brief-agent-memory-2026-10-20/brief.md) | `bmad-product-brief` (Update / Validate) |
| PRD | [prd.md](planning-artifacts/prds/prd-agent-memory-2026-10-20/prd.md) | `bmad-prd` (Update / Validate) |
| Architecture | [ARCHITECTURE-SPINE.md](planning-artifacts/architecture/architecture-agent-memory-2026-10-20/ARCHITECTURE-SPINE.md) | `bmad-architecture` (Update / Validate) |
| Epics & Stories | [epics.md](planning-artifacts/epics.md) | `bmad-create-epics-and-stories` |
| Sprint | `implementation-artifacts/sprint-status.yaml` (chưa có) | `bmad-sprint-planning` |

## Trạng thái

Tất cả artifact đang ở `status: draft`. Trước khi chuyển sang `final` cần đóng các câu hỏi mở:
- Q1–Q5 trong [prd.md §8](planning-artifacts/prds/prd-agent-memory-2026-10-20/prd.md);
- các giả định A1–A4 trong [brief addendum](planning-artifacts/briefs/brief-agent-memory-2026-10-20/addendum.md).

Hai spike Story 1.1 (FTS5) và Story 4.1 (model) chặn phần lớn công việc triển khai.
