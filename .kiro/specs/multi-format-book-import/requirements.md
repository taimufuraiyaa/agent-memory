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

### R6 - System-managed library identity

- The local dashboard does not display or request reader IDs or library IDs.
- When those IDs are omitted, the server derives stable opaque IDs from the workspace and library scope.
- The same workspace and personal scope reuse the same generated reader and library IDs across import, structure, query, and memory-review requests.
- Organization libraries continue to require an organization ID, and their generated library identity is stable for that workspace and organization.
- Existing clients may continue to send explicit reader and library IDs without behavior changes.

### R7 - Language selection

- The dashboard presents book language as a dropdown instead of an unrestricted text field.
- Options use stable BCP 47-style language tags and human-readable names, including native-script labels where useful.
- English remains the default.
- The selected tag is sent unchanged with both file uploads and pasted-text imports.
- Book content remains Unicode/UTF-8 regardless of the selected language; selection records language metadata and does not transcode source text.

### R8 - Optional local LLM setup

- Local inference uses a generic OpenAI-compatible HTTP endpoint so Ollama, LM Studio, vLLM, and equivalent local servers can share one contract.
- Local inference is disabled by default and must never be required for native-text PDF, EPUB, Markdown, or plain-text ingestion.
- Configuration is stored by the backend with restrictive file permissions; API keys are write-only and never returned to the dashboard.
- The backend exposes configured, enabled, and reachable states independently so the UI never treats saved but unreachable settings as operational.
- The initial rollout validates and stores the endpoint contract without claiming that enrichment or OCR has executed; those stages remain separately observable processing tasks.

### R9 - Parser fallback decision

- Before the first import without an operational local endpoint, the dashboard asks the reader to set up a local LLM, continue with the built-in parser, or cancel.
- Continuing with the built-in parser is the safe default and can be remembered on the current device.
- A configured but unreachable endpoint does not block parser-only import and produces an actionable status message.
- Fully scanned PDFs are never reported as completely read by the parser-only path; they remain rejected or explicitly OCR-pending until a vision/OCR processor is connected.

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
- Never: return stored local-model credentials, permit arbitrary remote URLs under a local-only policy, or imply that connectivity testing performed book enrichment.

## Success criteria

- A real native-text PDF and a valid EPUB can be uploaded through the HTTP endpoint and queried as indexed passages.
- The dashboard sends PDF/EPUB files as multipart data and shows their format.
- Existing Markdown workflow, repository tests, and embedded dashboard release checks pass.
- A dashboard user can complete the import, retrieval, and review flow without seeing or entering reader or library IDs.
- A dashboard user can choose a supported book language from a dropdown and import non-Latin Unicode content without text conversion.
- A reader can securely save and test a loopback OpenAI-compatible endpoint, see its actual reachability, and explicitly choose parser-only import when it is unavailable.

## Deferred

- Scanned-PDF OCR execution remains behind the existing configurable OCR boundary.
- Local-model summary, claim extraction, and vision/OCR execution remain later processing stages; this setup slice only establishes their provider contract and import policy.
- Web-book capture remains a URL/capture workflow with robots and immutable capture semantics; it is not modeled as a local file upload.
