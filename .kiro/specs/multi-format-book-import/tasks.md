# Multi-format Book Import Tasks

## Task M1 - Common format importer

- [x] Add failing EPUB/PDF persistence tests using the shared import contract.
- [x] Add a format-neutral importer with stable identity, deduplication, and locator rebinding.
- [x] Keep the existing Markdown importer behavior compatible.
- [x] Verify `go test ./internal/ingestion -run 'TestBookImport'`.

## Task M2 - Production native-text PDF extraction

- [x] Add a failing real-PDF extraction test.
- [x] Implement the in-process positioned-text extractor and parser version provenance.
- [x] Preserve page nodes, bounding boxes, and OCR-required pages.
- [x] Verify PDF and EPUB ingestion tests.

## Task M3 - Multipart library API

- [x] Add failing API tests for EPUB, PDF, unsupported formats, and upload limits.
- [x] Accept bounded multipart uploads while retaining legacy JSON Markdown requests.
- [x] Route all supported formats through the common importer and persist access policy only after success.
- [x] Verify focused API and security tests.

## Task M4 - Multi-format dashboard flow

- [x] Add failing dashboard contract coverage for PDF/EPUB selection and multipart transport.
- [x] Preserve binary `File` objects and keep pasted Markdown/text editable.
- [x] Show selected filename, format, and size; update supported-format guidance.
- [x] Verify dashboard tests, typecheck, and production build.

## Task M5 - Release gate

- [x] Rebuild embedded assets and verify relative/self-contained paths.
- [x] Smoke-test the production embedded dashboard.
- [x] Run repository-wide tests, race checks for touched packages, vet, portability, and diff checks.
- [x] Record durable knowledge and finish with a scoped commit that preserves pre-existing work.

## Task M6 - System-managed reader and library identity

- [x] Add failing backend tests for stable generated personal and organization identities while preserving explicit IDs.
- [x] Derive omitted identities consistently across import, structure, query, and memory review.
- [x] Add failing dashboard contract tests, remove reader/library fields, and omit their request parameters.
- [x] Verify focused backend tests, dashboard tests, typecheck, and the embedded production build.

## Task M7 - Unicode language selector

- [x] Add a failing dashboard contract test for a language dropdown with English default and representative Latin, CJK, RTL, and Indic options.
- [x] Replace free-text language entry with a BCP 47-tagged selector while preserving the existing import payload.
- [x] Synchronize embedded dashboard assets and verify dashboard tests, bundle syntax, and the production Go build.
