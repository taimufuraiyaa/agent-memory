---
title: "Addendum — PRD agent-memory BM25-only"
created: 2026-10-20
updated: 2026-10-20
---

# Addendum — Lựa chọn kỹ thuật và chi tiết đã cân nhắc

Phần này giữ các chi tiết kỹ thuật không thuộc PRD (capabilities). Chúng được chuyển sang architecture spine và solution design.

## Trọng số field mặc định (tinh chỉnh bằng benchmark)

| Field | Trọng số |
|---|---|
| identifiers | 4.0 |
| keywords | 3.0 |
| title | 2.0 |
| normalized_body | 1.0 |
| expansions | 0.6 |
| raw_body | 0.3 |

## Ngưỡng và tham số

| Tham số | Giá trị |
|---|---|
| Top N ứng viên BM25 | 200 |
| Deadline query normalizer | 150 ms |
| Ngưỡng văn bản lớn | 2k token (bất đồng bộ từ đây) |
| Trần cứng | 200k token hoặc 5 MB |
| Chunk | 400 token (150–800) |
| Ngân sách LLM mỗi tài liệu | 32 chunk hoặc 20 s CPU |
| Gộp điểm passage | `max + 0.3 × sum(top-3 còn lại)` |
| Near-dup | MinHash Jaccard ≥ 0.9 |

## Phương án đã loại

| Phương án | Lý do loại |
|---|---|
| Giữ hybrid BM25 + vector | Trái yêu cầu "hoàn toàn BM25"; vẫn mang ONNX |
| SPLADE | Vẫn cần inference nơ-ron mỗi lần ghi/hỏi |
| Chỉ query rewrite | Paraphrase một chiều, recall kém với câu hỏi ngắn |
| Chỉ doc expansion | Query tự nhiên nhiễu; mất `must_terms`/intent |
| Normalizer qua Ollama | Chính là mini LLM cần gỡ |
| Bleve/Tantivy | Chỉ mục ngoài SQLite, mất tính atomic; Tantivy cần cgo |
