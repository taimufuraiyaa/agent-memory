---
title: "Product Brief: agent-memory — BM25-only Retrieval với Embedded Normalizer"
status: draft
created: 2026-10-20
updated: 2026-10-20
---

# Product Brief: agent-memory — BM25-only Retrieval với Embedded Normalizer

## Executive Summary

agent-memory là lớp bộ nhớ cục bộ cho các AI coding agent. Hiện tại nó truy xuất bằng hai đường song song: embedding vector (ONNX MiniLM) và exact-term locator. Trong lõi còn có cả Jev harness/advisory lẫn một "mini LLM" (Ollama Qwen planner). Kết quả là kiến trúc nặng: phải tải runtime, có egress tuỳ chọn, nhiều package gắn chặt vào nhau, và vector làm mờ chính những identifier mà agent cần tìm nhất.

PR01 đưa agent-memory về **một cơ chế truy xuất duy nhất là BM25**. Keyword và identifier trở thành field có trọng số trong BM25F. Khả năng bắt paraphrase vốn do vector đảm nhận được thay bằng một **normalizer LLM nhỏ nhúng in-process**: nó chuẩn hoá token lúc ghi và chuẩn hoá câu hỏi lúc tìm, dùng cùng một version. Jev agent và mini LLM được tách khỏi lõi.

Kết quả mong đợi: lõi nhẹ hơn, chạy offline hoàn toàn, kết quả giải thích được tới từng term, và tìm identifier tốt hơn hẳn, trong khi chất lượng truy vấn paraphrase không giảm đáng kể.

## The Problem

- Agent hỏi "`harnessrun.Manager` recover thế nào" thì vector trả về các memory "gần nghĩa" chung chung, còn memory chứa đúng identifier lại bị xếp thấp.
- Exact-term chỉ phủ 3 locator mỗi memory, nên phần lớn nội dung không tìm được bằng từ khoá.
- Cài đặt phải kéo theo ONNX runtime/model, tuỳ chọn thêm Ollama và Qwen 4–14B. Build cgo/no-cgo phức tạp.
- Jev harness, Jev advisory và model routing nằm trong lõi, làm tăng attack surface, credential và egress của một daemon vốn chỉ cần lưu và tìm memory.
- Tài liệu (`storage/doc.go`, `engine/doc.go`) nói có FTS5, nhưng code không dùng. Người bảo trì bị hiểu sai.

## The Solution

- **Một chỉ mục BM25F** (SQLite FTS5) cho mọi truy vấn: `search`, `recall`, `solution recall`, `how-recall`.
- **Tokenizer hiểu identifier**: giữ token gốc đồng thời tách camel/snake/path; tiếng Việt lưu cả dạng có dấu và không dấu.
- **Normalizer LLM nhúng**:
  - lúc ghi: sinh `canonical_terms`, `identifiers`, `expansions` rồi mới lưu và index;
  - lúc tìm: viết lại câu hỏi thành truy vấn BM25 có cấu trúc.

  Output bị ràng buộc schema, giải mã xác định, luôn có rule-based fallback.
- **Văn bản lớn**: xử lý theo tầng kích thước (chunk theo cấu trúc, index theo passage, dedup, ngân sách LLM, hàng đợi bất đồng bộ, trần cứng).
- **Tách Jev và mini LLM**: harness/Jev thành module ngoài gọi lõi qua HTTP công khai; Ollama planner và `localllm` bị gỡ.

## What Makes This Different

- Lexical-first nhưng vẫn bắt được paraphrase, nhờ cùng một normalizer chạy ở cả hai đầu (doc expansion + query rewrite).
- Giải thích được ở mức term và field, việc vector không làm được.
- Offline tuyệt đối trong lõi; model normalizer được ghim hash.
- Đây là lợi thế về sự đơn giản và kỷ luật thiết kế, không phải một "công nghệ độc quyền".

## Who This Serves

- **AI coding agent** (Claude Code, Cursor, Codex…): cần tìm đúng identifier, đúng procedure, nhanh, kèm `request_id` để feedback.
- **Developer dùng agent-memory**: muốn cài nhẹ, không cần Ollama hay ONNX, gõ tiếng Việt có dấu hoặc không dấu đều tìm được.
- **Maintainer**: ít đường code song song, test xác định.

## Success Criteria

- Truy vấn identifier: Recall@10 ≥ baseline + 10 điểm %.
- Toàn bộ benchmark: Recall@10 không giảm quá 3 điểm %.
- Bộ paraphrase (≥ 200 cặp Anh/Việt): Recall@10 ≥ 85 %.
- p95 search ≤ 60 ms khi không gọi LLM, ≤ 250 ms khi cache miss có LLM; p95 write ≤ 400 ms (≤ 2 KB).
- Lõi không còn import `embeddings`, `harness*`, `jev*`; 0 egress mặc định.

## Scope

**Trong phạm vi:** chỉ mục BM25F, tokenizer, normalizer (ghi và tìm), xử lý văn bản lớn, tách Jev/mini LLM, migration dữ liệu cũ, benchmark paraphrase.

**Ngoài phạm vi:** thay đổi mô hình lifecycle; thiết kế lại dashboard; dịch vụ hosted; GraphRAG adapter (giữ nguyên, quyết định riêng về tuỳ chọn Ollama của nó).

## Vision

agent-memory trở thành bộ nhớ cục bộ "mở hộp là chạy": một binary, một chỉ mục lexical giải thích được, một model nhỏ chỉ làm đúng một việc. Các năng lực nặng hơn (harness, routing, advisory) là plugin tuỳ chọn bên ngoài lõi.
