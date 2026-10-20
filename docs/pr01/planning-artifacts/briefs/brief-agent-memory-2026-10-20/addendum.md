---
title: "Addendum — Brief agent-memory BM25-only"
created: 2026-10-20
updated: 2026-10-20
---

# Addendum

## Hiện trạng đã kiểm chứng trong code (`main@8892b13`)

| Thành phần | Hiện trạng | Vị trí |
|---|---|---|
| Truy xuất ngữ nghĩa | ONNX MiniLM → `memory_vectors` (BLOB) → cosine; cache lượng tử `TurbovecIndex` | `internal/embeddings`, `internal/storage/sqlite/vectors.go`, `turbovec.go`, `internal/engine/retrieval.go` (`VectorSearcher`) |
| Keyword / identifier | Exact-term locator (≤ 3/memory) trong `memory_terms`, Bloom loại trước | `internal/application/term_search.go`, `storage/sqlite/term_index.go` |
| BM25 / FTS5 | Không có trong code; chỉ được nhắc trong doc comment | `storage/doc.go`, `engine/doc.go` |
| Mini LLM | Ollama `127.0.0.1:11434`, `qwen3:8b` mặc định (4b/8b/14b); `localllm` cho library | `internal/bootstrap/ollama.go`, `internal/localllm`, `internal/cli/install_*.go` |
| Jev agent | `harness*`, `jev`, `jevconfig`, `harnessjev`, `harnessdecide`, listen bridge, subagent routing | `internal/harness*`, `internal/jev*`, `internal/cli/{harness_*,jev_*,listen_command}.go` |
| SQLite driver | `modernc.org/sqlite v1.50.0` | `go.mod` |

## Giả định cần xác nhận

| # | Giả định | Nếu sai |
|---|---|---|
| A1 | "mini LLO" = mini LLM (Ollama Qwen planner + `localllm`) | Phạm vi Epic 2 thay đổi |
| A2 | "Tách ra" = đưa ra module ngoài, chưa xoá vĩnh viễn | Nếu xoá hẳn thì Epic 2 đơn giản hơn nhưng mất tính năng |
| A3 | Normalizer là model nhỏ in-process, khác mini LLM cũ (dịch vụ ngoài, planner) | Nếu không chấp nhận LLM nào thì chỉ còn rule-based |
| A4 | `modernc.org/sqlite` có FTS5 và `bm25()` | Dùng chỉ mục postings tự viết |

## Rủi ro chính (chi tiết ở architecture)

- Paraphrase "xa" về từ vựng có thể kém hơn vector.
- Lệch normalizer giữa lúc index và lúc query.
- Chi phí CPU khi ingest văn bản lớn qua LLM.
- Migration `memory_vectors` và tương thích ngược API/MCP.
