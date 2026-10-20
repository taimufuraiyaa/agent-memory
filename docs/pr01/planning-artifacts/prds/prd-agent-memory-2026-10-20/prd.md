---
title: "PRD: agent-memory — BM25-only Retrieval với Embedded Normalizer"
status: draft
created: 2026-10-20
updated: 2026-10-20
inputs:
  - ../../briefs/brief-agent-memory-2026-10-20/brief.md
---

# PRD: agent-memory — BM25-only Retrieval với Embedded Normalizer

## 0. Document Purpose

Tài liệu này xác định năng lực cần có cho PR01: chuyển truy xuất sang BM25, chuẩn hoá bằng normalizer LLM nhúng, xử lý văn bản lớn, và tách Jev agent cùng mini LLM khỏi lõi. Lựa chọn kỹ thuật nằm trong [addendum.md](addendum.md) và architecture spine.

## 1. Vision

Một bộ nhớ cục bộ dùng một chỉ mục lexical duy nhất, giải thích được, tìm identifier chính xác, vẫn bắt được paraphrase, và không cần dịch vụ ngoài.

## 2. Target User

### 2.1 Jobs To Be Done
- Agent: "Trước khi tự nghiên cứu, tìm memory chứa đúng identifier/procedure tao cần, kèm lý do vì sao nó khớp."
- Developer: "Cài agent-memory mà không cần Ollama hay ONNX; gõ tiếng Việt không dấu vẫn tìm ra."
- Maintainer: "Một đường truy xuất, test xác định."

### 2.2 Key User Journeys
- **UJ-1. Agent tìm theo identifier.** Agent gọi `memory_search("harnessrun.Manager Recover")` → kết quả đứng đầu chứa đúng identifier; explain cho thấy điểm từ field `identifiers`. → FR-1, FR-4, FR-5
- **UJ-2. Agent hỏi kiểu diễn đạt khác.** "làm sao khởi động lại daemon" → normalizer sinh `restart`, `serve`, `daemon`, `--stop`, `--start` → tìm ra procedure `agent-memory serve --stop/--start`. → FR-9, FR-12
- **UJ-3. Developer ghi một transcript dài.** Ghi transcript 60k token → nhận `job_id` ngay; memory tìm được ngay bằng rule-based; paraphrase đầy đủ sau khi worker xử lý xong. → FR-15..FR-19
- **UJ-4. Developer nâng cấp.** `agent-memory upgrade` → chỉ mục BM25 được xây cho dữ liệu cũ, chạy tiếp được nếu bị ngắt; các lệnh Jev/harness báo `capability_removed` kèm hướng dẫn. → FR-6, FR-7, FR-20

## 3. Glossary

- **BM25F** — BM25 có trọng số theo field. Ở đây là FTS5 với trọng số theo cột.
- **Field** — một trong `identifiers`, `keywords`, `title`, `normalized_body`, `expansions`, `raw_body`.
- **Normalizer** — dịch vụ in-process (LLM nhỏ + rule-based fallback) sinh token chuẩn hoá cho doc và query.
- **normalizer_version** — định danh gồm hash model, prompt, grammar và tokenizer. Doc và query phải cùng version.
- **Passage** — chunk của văn bản lớn, là một doc riêng trong chỉ mục.
- **Fallback** — kết quả chuẩn hoá do rule-based tạo ra khi LLM lỗi, timeout hoặc bị tắt.

## 4. Features

### 4.1 Truy xuất BM25 duy nhất
**Description:** Mọi đường truy xuất lấy ứng viên từ một chỉ mục BM25F. Các tín hiệu xếp hạng hiện có được giữ nguyên, riêng tín hiệu semantic được thay bằng lexical. Realizes UJ-1, UJ-2.

#### FR-1: Ứng viên chỉ từ BM25
`search`, `recall`, `solution recall`, `how-recall` lấy ứng viên chỉ từ chỉ mục BM25F.
**Consequences (testable):** khi tắt hoàn toàn embedding, mọi test retrieval vẫn trả kết quả; `go list` của engine không import `embeddings`.

#### FR-2: Chỉ mục nhiều field có trọng số
Chỉ mục có 6 field với trọng số cấu hình được.
**Consequences:** đổi trọng số qua cấu hình làm thay đổi thứ hạng theo hướng dự đoán được (có test).

#### FR-3: Multi-signal rerank với tín hiệu lexical
Điểm BM25 được chuẩn hoá về 0..1 rồi thay tín hiệu `semantic`. Recency, outcome, decay, tier bias, salience và suppression giữ nguyên.
**Consequences:** explain có `lexical` và alias `semantic` trong một release.

#### FR-4: Explain theo field và term
Explain trả đóng góp BM25 theo field, các term đã khớp, và cách chuẩn hoá điểm.

#### FR-5: Exact mode tương đương
Truy vấn exact `AND`/`OR` trên locator cho cùng tập kết quả như hiện tại.
**Consequences:** bộ test `term_search` hiện có pass khi chạy trên chỉ mục mới.

#### FR-6: Graph expansion từ seed BM25
Graph expansion và recall assembly nhận seed từ top-k BM25.

### 4.2 Tách Jev agent và mini LLM
**Description:** Lõi không còn harness, Jev, model routing, Ollama planner hay `localllm`. Năng lực harness (nếu còn) chạy như module ngoài gọi HTTP công khai. Realizes UJ-4.

#### FR-7: Lõi build không có harness/Jev/mini LLM
Lõi build và chạy được khi không có các package `harness*`, `jev*`, `modelroute`, `openaiapi`, `bootstrap/ollama`, `localllm`.

#### FR-8: Bề mặt bị gỡ trả lỗi có hướng dẫn
Gọi route/tool/lệnh đã gỡ trả `capability_removed` kèm hướng dẫn, không trả 404 im lặng. `listen on|off|status` (chỉ recall) vẫn hoạt động. Credential Jev đã lưu không bị xoá; `doctor` báo cho người dùng.
**Out of Scope:** tính năng mới cho module harness ngoài.

### 4.3 Keyword và identifier
**Description:** Identifier và locator là công dân hạng nhất của chỉ mục. Realizes UJ-1.

#### FR-9: Tokenizer giữ token gốc và tách phần
Tách camelCase, snake_case, kebab, dot, path nhưng giữ token gốc; giữ nguyên mã lỗi và số phiên bản; không stem identifier.
**Consequences:** golden test ≥ 200 mẫu.

#### FR-10: Tiếng Việt có dấu và không dấu
Truy vấn không dấu khớp được nội dung có dấu và ngược lại; từ ghép được index dạng bigram âm tiết.

#### FR-11: Boost identifier trong truy vấn
Query có backtick hoặc khớp mẫu identifier được tăng trọng số field `identifiers`.

### 4.4 Chuẩn hoá lúc ghi
**Description:** Mọi nội dung đi qua normalizer trước storage engine. Nội dung gốc luôn được giữ nguyên. Realizes UJ-2, UJ-3.

#### FR-12: Normalize trước khi lưu
Memory, note, observation được promote, và chunk từ study/library đều đi qua normalizer. Output gồm `canonical_terms`, `identifiers`, `expansions`, `language`, `normalizer_version`.
**Consequences:** nội dung gốc trong `memories` giống hệt input.

#### FR-13: Output ràng buộc và xác định
Output đúng schema 100 %; identifier phải xuất hiện trong input; cùng input và cùng version thì cho cùng output.

#### FR-14: Fallback và chuẩn hoá lại
LLM lỗi/timeout/tắt thì dùng rule-based, gắn `normalization=fallback`, đưa vào hàng đợi chuẩn hoá lại. Khi đổi version thì reindex nền.

### 4.5 Chuẩn hoá câu hỏi
**Description:** Câu hỏi được viết lại thành truy vấn BM25 có cấu trúc bằng cùng normalizer. Realizes UJ-2.

#### FR-15: Query rewrite có deadline
Sinh `must_terms`, `canonical_terms`, `expansions`, `identifiers`, `intent` trong deadline; quá hạn thì dùng rule-based. Truy vấn không bao giờ thất bại vì LLM.

#### FR-16: Fast path và cache
Query chỉ gồm identifier/keyword hoặc ≤ 3 token bỏ qua LLM. Query đã chuẩn hoá được cache theo `(normalizer_version, query)`.

#### FR-17: Explain query đã chuẩn hoá
Response có `normalized_query` và cờ `normalization`.

### 4.6 Văn bản lớn
**Description:** Văn bản lớn được chia, dedup và chuẩn hoá trong ngân sách, xử lý bất đồng bộ. Realizes UJ-3.

#### FR-18: Xử lý theo tầng kích thước
≤ 2k token: đồng bộ. 2k–200k: chunk theo cấu trúc, index theo passage, dedup, ngân sách LLM, bất đồng bộ. > 200k hoặc > 5 MB: từ chối với `input_too_large` và hướng dẫn chuyển sang `study`/library import.

#### FR-19: Job bất đồng bộ
Trả `job_id`, có tiến độ, huỷ được, chạy tiếp được sau restart. Memory tìm được ngay bằng rule-based index.

#### FR-20: Recall theo passage
Recall trả về passage khớp kèm lân cận trong token budget; điểm passage được gộp về memory.

### 4.7 Migration
#### FR-21: Xây chỉ mục cho dữ liệu cũ
`upgrade` xây chỉ mục BM25 idempotent, có cursor để chạy tiếp; `memory_vectors` giữ read-only một release để rollback.

## 5. Non-Goals (Explicit)

- Không giữ hybrid BM25 + vector lâu dài.
- Không thêm dịch vụ hosted hay egress trong lõi.
- Không thay đổi mô hình lifecycle.
- Không viết tóm tắt văn bản bằng LLM (chỉ extractive).

## 6. MVP Scope

### 6.1 In Scope
FR-1..FR-21.

### 6.2 Out of Scope for MVP
SPLADE/sparse learned expansion; tuỳ chỉnh BM25F thật với k1/b theo field (chỉ làm nếu spike FTS5 thất bại); module harness ngoài có tính năng mới.

## 7. Success Metrics

**Primary**
- **SM-1**: Recall@10 truy vấn identifier ≥ baseline + 10 điểm %. Validates FR-9, FR-11.
- **SM-2**: Recall@10 bộ paraphrase (≥ 200 cặp Anh/Việt) ≥ 85 %. Validates FR-12, FR-15.
- **SM-3**: Recall@10 toàn benchmark không giảm quá 3 điểm %. Validates FR-1..FR-6.

**Secondary**
- **SM-4**: p95 search ≤ 60 ms (không LLM), ≤ 250 ms (LLM, cache miss). Validates FR-16.
- **SM-5**: p95 write ≤ 400 ms (≤ 2 KB). Validates FR-12.
- **SM-6**: Cài đặt sạch không có ONNX/Ollama; 0 egress lõi. Validates FR-7.

**Counter-metrics (do not optimize)**
- **SM-C1**: Số expansions mỗi doc. Không tối đa hoá vì làm nhiễu BM25 và phình chỉ mục. Counterbalances SM-2.
- **SM-C2**: Tỉ lệ dùng LLM. Không ép lên 100 % vì fast path và fallback là có chủ đích. Counterbalances SM-4.

## 8. Open Questions

| # | Câu hỏi | Mặc định đề xuất | Chặn phase? |
|---|---|---|---|
| Q1 | Jev/harness: tách module ngoài hay xoá hẳn? | Tách, đóng băng bảo trì | Không |
| Q2 | Chấp nhận cgo (llama.cpp) cho build mặc định? | Có trên macOS/Linux; Windows dùng rule-based | Có, chặn E4 |
| Q3 | Model normalizer cụ thể và giới hạn RAM | Chọn qua spike, ≤ 1 GB file, ≤ 1.5 GB RAM | Có, chặn E4 |
| Q4 | Tuỳ chọn Ollama của GraphRAG có bị gỡ cùng mini LLM? | Giữ adapter Python, bỏ tuỳ chọn Ollama | Không |
| Q5 | Tính năng dịch của library (`localllm/translate`) | Bỏ ở PR01 | Không |

## 9. Assumptions Index

- [ASSUMPTION: "mini LLO" là mini LLM = Ollama Qwen planner + `localllm`] — FR-7, FR-8
- [ASSUMPTION: "tách" nghĩa là chuyển ra module ngoài] — FR-7, FR-8
- [ASSUMPTION: normalizer in-process được phép dù mini LLM bị gỡ] — FR-12..FR-17
- [ASSUMPTION: driver `modernc.org/sqlite` có FTS5 + `bm25()`] — FR-1, FR-2

## Cross-Cutting NFRs

| ID | Yêu cầu |
|---|---|
| NFR-1 | Offline 100 % trong lõi |
| NFR-2 | Xác định: cùng input và cùng version thì cùng output (golden test trên macOS và Linux) |
| NFR-3 | RAM normalizer ≤ 1.5 GB khi nạp; unload khi idle |
| NFR-4 | Tương thích ngược `memory_*`, `solution_*`, `skill_*`: chỉ được thêm field |
| NFR-5 | Không lưu chain-of-thought của LLM, chỉ lưu output có cấu trúc |
| NFR-6 | Redaction chạy trước normalizer; secret không lọt vào `expansions` |

## Risk and Mitigations

| Rủi ro | Giảm thiểu |
|---|---|
| Paraphrase kém hơn vector | SM-2 làm cổng release; flag rollback về vector trong 1 release |
| Lệch normalizer giữa index và query | `normalizer_version` ở mọi doc và cache; reindex nền |
| LLM chậm trên máy yếu | Fast path, cache, deadline, fallback, cấu hình tắt LLM |
| Người dùng phụ thuộc harness/Jev | Module ngoài, deprecation trước một release, `doctor` hướng dẫn |

## Rollout and Change Management

P0 spike → P1 shadow → P2 opt-in flag → P3 BM25 mặc định (vector read-only) → P4 gỡ vector, embeddings, Bloom và tách harness/Jev. Cổng giữa các giai đoạn là SM-1..SM-5.
