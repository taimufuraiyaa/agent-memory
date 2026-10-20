---
status: draft
stepsCompleted: []
inputDocuments:
  - briefs/brief-agent-memory-2026-10-20/brief.md
  - prds/prd-agent-memory-2026-10-20/prd.md
  - architecture/architecture-agent-memory-2026-10-20/ARCHITECTURE-SPINE.md
  - architecture/architecture-agent-memory-2026-10-20/solution-design.md
  - architecture/architecture-agent-memory-2026-10-20/large-text-input.md
---

# agent-memory - Epic Breakdown

## Overview

Tài liệu này chia PR01 thành epic và story có thể triển khai được. Nguồn: PRD, Architecture Spine (AD-1..AD-11) và solution design. Không có tài liệu UX.

## Requirements Inventory

### Functional Requirements

- FR-1 Ứng viên chỉ từ BM25
- FR-2 Chỉ mục nhiều field có trọng số
- FR-3 Multi-signal rerank với tín hiệu lexical
- FR-4 Explain theo field và term
- FR-5 Exact mode tương đương
- FR-6 Graph expansion từ seed BM25
- FR-7 Lõi build không có harness/Jev/mini LLM
- FR-8 Bề mặt bị gỡ trả lỗi có hướng dẫn
- FR-9 Tokenizer giữ token gốc và tách phần
- FR-10 Tiếng Việt có dấu và không dấu
- FR-11 Boost identifier trong truy vấn
- FR-12 Normalize trước khi lưu
- FR-13 Output ràng buộc và xác định
- FR-14 Fallback và chuẩn hoá lại
- FR-15 Query rewrite có deadline
- FR-16 Fast path và cache
- FR-17 Explain query đã chuẩn hoá
- FR-18 Xử lý theo tầng kích thước
- FR-19 Job bất đồng bộ
- FR-20 Recall theo passage
- FR-21 Xây chỉ mục cho dữ liệu cũ

### NonFunctional Requirements

- NFR-1 Offline 100 % trong lõi
- NFR-2 Xác định (golden test macOS + Linux)
- NFR-3 RAM normalizer ≤ 1.5 GB, unload khi idle
- NFR-4 Tương thích ngược, chỉ thêm field
- NFR-5 Không lưu chain-of-thought
- NFR-6 Redaction trước normalizer

### Additional Requirements

- Architecture spine AD-1..AD-11 là ràng buộc bắt buộc cho mọi story.
- Spike FTS5 (Story 1.1) và spike model (Story 4.1) chặn các story phụ thuộc.
- Open questions Q2 (cgo) và Q3 (model) chặn Epic 4.

### UX Design Requirements

N/A — không có thay đổi UX ngoài các trường explain mới trong dashboard.

### FR Coverage Map

| FR | Story |
|---|---|
| FR-1 | 1.3 |
| FR-2 | 1.2, 1.3 |
| FR-3 | 1.4 |
| FR-4 | 1.3, 5.4 |
| FR-5 | 1.5 |
| FR-6 | 1.6 |
| FR-7 | 2.1, 2.3, 2.5, 7.5 |
| FR-8 | 2.4, 2.6 |
| FR-9 | 3.1 |
| FR-10 | 3.2 |
| FR-11 | 3.3 |
| FR-12 | 4.4 |
| FR-13 | 4.2, 4.6 |
| FR-14 | 4.3, 4.5 |
| FR-15 | 5.1 |
| FR-16 | 5.2, 5.3 |
| FR-17 | 5.4 |
| FR-18 | 6.1, 6.4, 6.7 |
| FR-19 | 6.5 |
| FR-20 | 6.2, 6.6 |
| FR-21 | 7.2 |

## Epic List

1. Epic 1: Chỉ mục BM25F và retrieval engine
2. Epic 2: Tách Jev agent và mini LLM
3. Epic 3: Tokenizer identifier-aware
4. Epic 4: Embedded Normalizer lúc ghi
5. Epic 5: Query Normalizer
6. Epic 6: Large text ingestion
7. Epic 7: Migration, benchmark và rollout

## Epic 1: Chỉ mục BM25F và retrieval engine

Mọi truy xuất lấy ứng viên từ một chỉ mục FTS5 nhiều field, điểm được chuẩn hoá và rerank bằng các tín hiệu hiện có (AD-1, AD-2, AD-3).

### Story 1.1: Spike FTS5 trên modernc.org/sqlite

As a maintainer,
I want to xác nhận FTS5 và `bm25()` có trọng số theo cột chạy được trên driver hiện tại,
So that ta chọn đúng giữa FTS5 và chỉ mục postings tự viết.

**Acceptance Criteria:**

**Given** driver `modernc.org/sqlite v1.50.0`
**When** tạo bảng FTS5 6 cột, ghi 50k doc và chạy `MATCH` với `bm25(w1..w6)`
**Then** truy vấn trả kết quả đúng và latency p95 được ghi lại
**And** kết luận (FTS5 hoặc postings) được ghi vào memlog của architecture và cập nhật spine nếu cần

### Story 1.2: Schema chỉ mục

As a maintainer,
I want có các bảng `memory_index_docs`, `memory_fts` (external content) và `index_state`,
So that chỉ mục được cập nhật atomic cùng dữ liệu.

**Acceptance Criteria:**

**Given** một DB workspace hiện có
**When** chạy migration hai lần
**Then** schema giống nhau (idempotent)
**And** một write bị rollback không để lại dòng nào trong FTS

### Story 1.3: BM25 search adapter thay VectorSearcher

As an AI agent,
I want ứng viên được lấy từ BM25 kèm điểm theo field,
So that kết quả giải thích được và đúng identifier.

**Acceptance Criteria:**

**Given** chỉ mục đã có dữ liệu
**When** gọi search
**Then** trả top N ứng viên với `bm25_fields` và `matched_terms`
**And** điểm được chuẩn hoá về 0..1 và cách chuẩn hoá có trong explain

### Story 1.4: Multi-signal scoring với tín hiệu lexical

As an AI agent,
I want các tín hiệu recency, outcome, decay, tier bias, salience, suppression vẫn hoạt động,
So that thứ hạng không đổi bản chất.

**Acceptance Criteria:**

**Given** cấu hình adaptive policy hiện có
**When** chạy search hoặc recall
**Then** explain có `lexical` và alias `semantic`
**And** `min_bm25_norm` thay `min_semantic_score` với test xác định

### Story 1.5: Exact mode qua BM25

As an AI agent,
I want truy vấn `AND`/`OR` trên locator cho kết quả như trước,
So that workflow hiện có không bị vỡ.

**Acceptance Criteria:**

**Given** bộ test `term_search` hiện có
**When** chạy trên chỉ mục FTS5
**Then** mọi test pass với cùng tập kết quả

### Story 1.6: Graph expansion và recall assembly dùng seed BM25

As an AI agent,
I want recall vẫn mở rộng theo graph và tôn trọng token budget,
So that context block giữ nguyên chất lượng.

**Acceptance Criteria:**

**Given** seed là top-k BM25
**When** chạy recall với graph-expand
**Then** các test `recall_graph_*` và `recall_assembly` pass

### Story 1.7: Shadow mode

As a maintainer,
I want BM25 chạy song song với vector mà không ảnh hưởng kết quả,
So that ta đo được overlap trước khi chuyển.

**Acceptance Criteria:**

**Given** `search.shadow_bm25=true`
**When** có truy vấn
**Then** kết quả trả về vẫn từ vector
**And** metric overlap@10 và latency BM25 được ghi vào `/metrics`

## Epic 2: Tách Jev agent và mini LLM

Lõi không còn harness, Jev, model routing, Ollama planner và `localllm`; năng lực harness (nếu giữ) là module ngoài (AD-10, AD-11).

### Story 2.1: Kiểm kê phụ thuộc

As a maintainer,
I want danh sách đầy đủ các import tới `harness*`, `jev*`, `modelroute`, `openaiapi`, `localllm`, `bootstrap/ollama`,
So that việc tách không bỏ sót.

**Acceptance Criteria:**

**Given** `go list` của lõi
**When** sinh báo cáo phụ thuộc
**Then** báo cáo được lưu trong `docs/pr01/implementation-artifacts/`

### Story 2.2: Extension registry và capability

As a module author,
I want khai báo capability qua `/api/v1/capabilities`,
So that client biết module ngoài có sẵn hay không.

**Acceptance Criteria:**

**Given** một module ngoài đăng ký capability
**When** gọi `/api/v1/capabilities`
**Then** capability xuất hiện
**And** lõi không import module ngoài

### Story 2.3: Chuyển harness/Jev sang module agent-memory-harness

As a maintainer,
I want harness/Jev nằm trong module riêng chỉ gọi HTTP công khai,
So that lõi gọn và không còn egress.

**Acceptance Criteria:**

**Given** module mới
**When** build độc lập
**Then** các test harness hiện có pass trong module mới

### Story 2.4: Gỡ route, tool, CLI khỏi lõi

As a developer,
I want nhận lỗi có hướng dẫn khi gọi tính năng đã gỡ,
So that tao biết phải làm gì tiếp.

**Acceptance Criteria:**

**Given** lõi không có module harness
**When** gọi `/api/v1/harness/*`, MCP `harness_*`, `listen jev-*` hoặc `harness …`
**Then** nhận `capability_removed` kèm hướng dẫn
**And** `listen on|off|status` (chỉ recall) vẫn hoạt động

### Story 2.5: Gỡ Ollama planner và localllm

As a developer,
I want cài đặt không còn bước Ollama,
So that cài nhanh và nhẹ.

**Acceptance Criteria:**

**Given** installer mới
**When** chạy install hoặc upgrade
**Then** không còn `--with-local-llm`, `--local-llm-model` hay bước Ollama
**And** `doctor` báo credential Jev và cấu hình Ollama còn sót mà không xoá

### Story 2.6: MCP adapter ẩn tool theo capability

As an AI agent,
I want chỉ thấy tool đang thực sự dùng được,
So that không gọi nhầm tool đã gỡ.

**Acceptance Criteria:**

**Given** không có capability harness
**When** MCP handshake
**Then** không có `harness_*` trong danh sách tool và test stdio pass

## Epic 3: Tokenizer identifier-aware

Identifier, path, mã lỗi và tiếng Việt được token hoá đúng, có version (AD-2, AD-3).

### Story 3.1: Tokenizer Go có version

As an AI agent,
I want identifier được giữ nguyên đồng thời được tách phần,
So that tìm theo tên đầy đủ hay theo từng phần đều ra.

**Acceptance Criteria:**

**Given** golden set ≥ 200 mẫu (camel, snake, kebab, dot, path, mã lỗi, version)
**When** token hoá
**Then** output khớp golden 100 %
**And** `tokenizer_version` được ghi vào `index_state`

### Story 3.2: Tiếng Việt có dấu và không dấu

As a developer,
I want gõ không dấu vẫn tìm được nội dung có dấu,
So that tìm kiếm tiếng Việt tự nhiên.

**Acceptance Criteria:**

**Given** bộ mẫu tiếng Việt
**When** truy vấn có dấu hoặc không dấu
**Then** độ chính xác ≥ 95 %

### Story 3.3: Boost identifier trong truy vấn

As an AI agent,
I want query chứa identifier được ưu tiên field `identifiers`,
So that kết quả đúng identifier đứng đầu.

**Acceptance Criteria:**

**Given** benchmark identifier
**When** chạy với boost
**Then** Recall@10 ≥ baseline + 10 điểm % (SM-1)

## Epic 4: Embedded Normalizer lúc ghi

Mọi nội dung được chuẩn hoá bởi normalizer in-process trước khi lưu, có fallback và version (AD-4..AD-8).

### Story 4.1: Spike chọn runtime và model

As a maintainer,
I want so sánh ít nhất 3 model nhỏ (0.5–1.5B, Q4),
So that chọn được model đủ tốt, đủ nhanh, giấy phép hợp lệ.

**Acceptance Criteria:**

**Given** bộ mẫu Anh/Việt
**When** đo chất lượng expansions, token/s CPU, RAM, giấy phép
**Then** model và runtime được chốt, hash được ghim
**And** Q2, Q3 trong PRD được đóng

### Story 4.2: Normalizer service với constrained decoding

As a maintainer,
I want output luôn đúng schema và an toàn,
So that không có dữ liệu rác hay injection vào chỉ mục.

**Acceptance Criteria:**

**Given** input bất kỳ (kể cả nội dung chứa lệnh giả)
**When** normalize
**Then** output khớp schema 100 %, identifier ngoài input bị loại, độ dài bị giới hạn
**And** có timeout, semaphore và unload khi idle

### Story 4.3: Rule-based fallback

As a developer,
I want ghi luôn thành công kể cả khi model lỗi,
So that memory không bao giờ bị mất.

**Acceptance Criteria:**

**Given** LLM lỗi, timeout hoặc bị tắt
**When** ghi
**Then** dùng rule-based và gắn `normalization=fallback`

### Story 4.4: Tích hợp vào write pipeline

As an AI agent,
I want mọi write đi qua redaction rồi normalizer rồi mới lưu,
So that chỉ mục bắt được paraphrase và không chứa secret.

**Acceptance Criteria:**

**Given** memory ≤ 2 KB
**When** ghi
**Then** nội dung gốc không đổi và p95 ≤ 400 ms (SM-5)
**And** secret đã redact không xuất hiện trong `expansions`

### Story 4.5: Normalization queue và reindex theo version

As a maintainer,
I want doc fallback và doc khác version được chuẩn hoá lại ở nền,
So that chỉ mục hội tụ về trạng thái đầy đủ.

**Acceptance Criteria:**

**Given** có doc fallback hoặc version thay đổi
**When** worker chạy
**Then** doc được chuẩn hoá lại theo lô, resume được sau restart, tiến độ có trong `/metrics`

### Story 4.6: Golden test xác định

As a maintainer,
I want cùng input và cùng version thì cho cùng output,
So that index và query không lệch.

**Acceptance Criteria:**

**Given** golden set
**When** chạy 3 lần trên macOS và Linux
**Then** output giống hệt nhau

### Story 4.7: Build tags normalizer_llm và normalizer_rules

As a packager,
I want có bản pure Go không cần cgo,
So that phát hành được trên mọi nền tảng.

**Acceptance Criteria:**

**Given** CI
**When** build cả hai biến thể
**Then** cả hai pass test chức năng; bản `normalizer_rules` dùng rule-based

## Epic 5: Query Normalizer

Câu hỏi được viết lại thành truy vấn BM25 có cấu trúc bằng cùng normalizer (AD-4, AD-6).

### Story 5.1: Query rewrite có deadline

As an AI agent,
I want câu hỏi tự nhiên được chuyển thành must/should terms,
So that tìm được memory diễn đạt khác.

**Acceptance Criteria:**

**Given** query tự nhiên
**When** search
**Then** sinh `must_terms`, `canonical_terms`, `expansions`, `identifiers`, `intent` trong 150 ms
**And** quá hạn thì dùng rule-based và request vẫn thành công

### Story 5.2: Fast path

As an AI agent,
I want query ngắn hoặc chỉ có identifier không phải chờ LLM,
So that tra cứu nhanh.

**Acceptance Criteria:**

**Given** query ≤ 3 token hoặc chỉ gồm identifier/keyword
**When** search
**Then** không gọi LLM và p95 ≤ 60 ms (SM-4)

### Story 5.3: normalized_query_cache

As a maintainer,
I want query đã chuẩn hoá được cache theo version,
So that giảm chi phí LLM.

**Acceptance Criteria:**

**Given** cùng query và cùng version
**When** search lần hai
**Then** cache hit, tỉ lệ hit có trong `/metrics`
**And** entry khác version bị bỏ qua

### Story 5.4: Explain

As an AI agent,
I want thấy query đã được chuẩn hoá thành gì,
So that hiểu vì sao kết quả khớp.

**Acceptance Criteria:**

**Given** search với explain
**When** nhận response
**Then** có `normalized_query`, `bm25_fields`, `normalization`

### Story 5.5: Intent điều chỉnh weights

As an AI agent,
I want câu hỏi "how/debug/outcome" được xếp hạng theo đúng mục đích,
So that procedure và outcome liên quan nổi lên.

**Acceptance Criteria:**

**Given** bảng cấu hình intent → weights
**When** intent được nhận diện
**Then** weights tương ứng được áp dụng (có test)

## Epic 6: Large text ingestion

Văn bản lớn được chia, dedup, chuẩn hoá trong ngân sách và xử lý bất đồng bộ (AD-9).

### Story 6.1: Structure-aware chunker với fallback sliding window

As a developer,
I want transcript, Markdown và log được chia theo cấu trúc,
So that mỗi passage có nghĩa.

**Acceptance Criteria:**

**Given** Markdown, transcript, log và văn bản thô
**When** chunk
**Then** passage dài 150–800 token và không cắt ngang khối code

### Story 6.2: Bảng passages và gộp điểm

As an AI agent,
I want nhận đúng đoạn liên quan thay vì cả tài liệu,
So that tiết kiệm token budget.

**Acceptance Criteria:**

**Given** memory nhiều passage
**When** recall
**Then** trả passage khớp kèm lân cận trong budget; công thức gộp cấu hình được

### Story 6.3: Dedup hash và MinHash

As a maintainer,
I want chunk trùng không bị index lặp,
So that chỉ mục gọn và TF không bị nhiễu.

**Acceptance Criteria:**

**Given** log lặp
**When** ingest
**Then** chunk trùng chính xác bị bỏ; near-dup Jaccard ≥ 0.9 bị gộp và danh sách offset được giữ lại

### Story 6.4: Ngân sách LLM và ưu tiên chunk

As a maintainer,
I want chi phí LLM mỗi tài liệu có trần,
So that ingest không chiếm hết CPU.

**Acceptance Criteria:**

**Given** tài liệu nhiều chunk
**When** ingest
**Then** không vượt 32 chunk hoặc 20 s CPU; phần còn lại vào hàng đợi

### Story 6.5: Ingest bất đồng bộ

As a developer,
I want ghi văn bản lớn không bị block,
So that agent tiếp tục làm việc ngay.

**Acceptance Criteria:**

**Given** input 2k–200k token
**When** ghi
**Then** nhận `job_id` ngay, có tiến độ, huỷ được, resume sau restart
**And** memory tìm được ngay qua rule-based index

### Story 6.6: Hồ sơ tài liệu

As an AI agent,
I want tài liệu dài tìm được bằng khái niệm tổng quát,
So that không bỏ sót tài liệu chỉ vì khớp rải rác.

**Acceptance Criteria:**

**Given** tài liệu đã chunk
**When** ingest xong
**Then** có doc cấp tài liệu với extractive summary và terms đã gộp

### Story 6.7: Trần cứng

As a maintainer,
I want input quá lớn bị từ chối có hướng dẫn,
So that daemon không cạn tài nguyên.

**Acceptance Criteria:**

**Given** input > 200k token hoặc > 5 MB
**When** ghi
**Then** nhận `input_too_large` kèm hướng dẫn dùng `study`/library import

## Epic 7: Migration, benchmark và rollout

Dữ liệu cũ chuyển sang chỉ mục mới, có cổng chất lượng và gỡ dần thành phần cũ (AD-8, AD-10, AD-11).

### Story 7.1: Bộ benchmark paraphrase

As a maintainer,
I want ≥ 200 cặp câu hỏi diễn đạt khác (Anh và Việt),
So that đo được SM-2.

**Acceptance Criteria:**

**Given** `evaluation/`
**When** chạy target `make`
**Then** báo Recall@10 cho bộ paraphrase

### Story 7.2: Migration dữ liệu cũ

As a developer,
I want `upgrade` xây chỉ mục BM25 cho dữ liệu hiện có,
So that không mất memory.

**Acceptance Criteria:**

**Given** workspace có dữ liệu vector
**When** chạy `upgrade` và bị ngắt giữa chừng
**Then** chạy lại sẽ tiếp tục từ cursor; `memory_vectors` được giữ read-only

### Story 7.3: Thay embedding similarity trong consolidation và conflict

As a maintainer,
I want lifecycle không còn cần embedding,
So that có thể gỡ `embeddings`.

**Acceptance Criteria:**

**Given** consolidation và conflict
**When** chạy với Jaccard/MinHash trên `canonical_terms`
**Then** test lifecycle pass

### Story 7.4: Cổng chuyển giai đoạn P1 → P4

As a maintainer,
I want mỗi giai đoạn rollout có cổng đo được,
So that không chuyển khi chất lượng chưa đạt.

**Acceptance Criteria:**

**Given** SM-1..SM-5
**When** chuẩn bị chuyển giai đoạn
**Then** chỉ chuyển khi tất cả đạt; kết quả được ghi vào implementation-artifacts

### Story 7.5: Gỡ vector, embeddings, turbovec, Bloom, reembed

As a maintainer,
I want lõi không còn mã vector,
So that lõi gọn và không cần ONNX.

**Acceptance Criteria:**

**Given** P3 đã ổn định một release
**When** gỡ
**Then** `go list` của lõi không còn import các package này; cài sạch không có ONNX runtime

### Story 7.6: Tài liệu và deprecation

As a developer,
I want tài liệu khớp với hệ thống,
So that không bị hiểu sai.

**Acceptance Criteria:**

**Given** thay đổi PR01
**When** release
**Then** README, `docs/architecture/*`, IDE rules và CHANGELOG được cập nhật
**And** phần nhắc FTS5 bị lệch trong `storage/doc.go` và `engine/doc.go` được sửa
