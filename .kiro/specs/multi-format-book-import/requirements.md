# Multi-format Book Import Requirements

## Objective

Extend the existing Living Knowledge Library import workflow so a reader can upload a complete PDF, EPUB, Markdown, or plain-text book from the dashboard and receive the same stable work, edition, source-asset, structure, passage, locator, and citation behavior.

## Requirements

### R1 - Format-aware upload

- The dashboard accepts `.pdf`, `.epub`, `.md`, `.markdown`, and `.txt` files.
- Binary files are transmitted without browser text decoding.
- Pasted Markdown/plain text remains supported for compatibility.
- The server determines the parser from an explicit supported format and validates the file signature where the format provides one.

### R2 - Whole-book extraction

- EPUB imports follow package metadata and spine reading order and preserve EPUB locators.
- Native-text PDF imports preserve page numbers and positioned text blocks.
- PDF pages without extractable native text are marked as requiring OCR and are not represented as empty author claims.
- Markdown and text imports retain heading/offset locators.

### R3 - Common persistence contract

- Every successful format import creates or reuses stable work, edition, and asset identities.
- Structural nodes and passages reference the final edition and source asset.
- Re-importing identical bytes is idempotent and refreshes policy without duplicating the edition.
- Import failure does not publish a completed job or source-access policy.

### R4 - Transport safety

- Multipart uploads have a bounded request size and clean temporary form files.
- Unsupported formats, malformed sources, missing metadata, and oversized uploads return actionable validation errors.
- Existing JSON Markdown clients remain compatible.

### R5 - User-visible provenance

- The dashboard displays the selected format and accepts binary files without placing their contents in a textarea.
- Indexed contents and retrieved evidence continue to display format-specific source locators.
- No uploaded source is copied into durable memory without the existing proposal and review workflow.

## Commands

- Backend tests: `go test ./internal/ingestion ./internal/api -run 'TestBookImport|TestLibraryImport'`
- Dashboard tests: `npm --prefix tools/agent-memory/dashboard test`
- Typecheck: `npm --prefix tools/agent-memory/dashboard run typecheck`
- Production build: `make build-with-dashboard`
- Full verification: `go test ./... && go vet ./...`

## Boundaries

- Always: preserve exact locator provenance, authorize before retrieval, and keep legacy Markdown JSON import working.
- Ask first: introduce a hosted parser/OCR provider or transmit source bytes to a third party.
- Never: treat OCR output as verified native text, infer author claims from missing pages, or compile a device-specific path.

## Success criteria

- A real native-text PDF and a valid EPUB can be uploaded through the HTTP endpoint and queried as indexed passages.
- The dashboard sends PDF/EPUB files as multipart data and shows their format.
- Existing Markdown workflow, repository tests, and embedded dashboard release checks pass.

## Deferred

- Scanned-PDF OCR execution remains behind the existing configurable OCR boundary.
- Web-book capture remains a URL/capture workflow with robots and immutable capture semantics; it is not modeled as a local file upload.
