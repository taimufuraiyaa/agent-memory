# Multi-format Book Import Design

## 1. Decision

Add a format-neutral persistence importer above the existing deterministic adapters. Keep the legacy Markdown importer as a compatibility wrapper, and add production EPUB and native-text PDF extraction paths. The API accepts both legacy JSON and multipart form data; the dashboard uses multipart when a file is selected.

## 2. Flow

```mermaid
flowchart TD
    File["User selects PDF, EPUB, Markdown, or text"] --> Upload["Bounded multipart upload"]
    Paste["User pastes Markdown or text"] --> Legacy["JSON compatibility request"]
    Upload --> Detect["Validate declared format and signature"]
    Legacy --> Detect
    Detect --> Adapter{"Format adapter"}
    Adapter -->|"PDF"| PDF["Page and positioned-text extraction"]
    Adapter -->|"EPUB"| EPUB["Package, spine, and XHTML extraction"]
    Adapter -->|"Markdown or text"| Text["Heading and offset extraction"]
    PDF --> Common["Normalized document contract"]
    EPUB --> Common
    Text --> Common
    Common --> Identity["Stable work, edition, and asset identity"]
    Identity --> Persist["Structure and passages with locators"]
    Persist --> Policy["Authorized library resource policies"]
    Policy --> UI["Contents, evidence, citations, and review"]
```

## 3. Extraction contract

The format-neutral importer consumes source bytes plus title, edition label, language, format, and policy. An extractor returns normalized text, structural nodes, passages, parser version, and normalization version. Extraction runs before publication so malformed sources cannot create partial library records.

PDF extraction uses an in-process, pure-Go parser that opens uploaded bytes and returns positioned text elements per page. Each page becomes a structural node. Contiguous text elements become citable blocks with page and bounding-box locators. A page with no native text remains a page node and is marked `requires_ocr`; no passage is fabricated for it.

EPUB extraction uses the existing ZIP/XML adapter, with package metadata and spine order authoritative. Final passage identities are rebound to the stable edition and asset rather than provisional extraction identities.

## 4. Persistence and identity

The byte fingerprint detects exact asset re-import. The normalized content fingerprint identifies an edition of a work. Work identity uses normalized title. Asset identity uses exact bytes. Before persistence, every node and passage is validated against the final edition and asset IDs.

Writes follow the existing order: work, edition, asset, structure, passages, then library resource policies. Existing storage methods remain the source of truth. A future transaction boundary can make publication fully atomic across all records; this slice ensures extraction and validation complete before the first write.

Reader and library scope IDs are transport concerns rather than book metadata. When a client omits them, the server derives deterministic opaque IDs with the existing SHA-256-based identifier helper. A reader ID is scoped to the workspace. A personal-library ID is scoped to the workspace and effective reader; an organization-library ID is scoped to the workspace and organization. The same derivation is applied to import, structure, query, and memory-review handlers so the dashboard never has to receive or persist these internal identifiers.

Explicit IDs remain authoritative for compatibility with API clients and organization workflows. Generated IDs do not create a new authentication boundary; the service remains local-first and continues to rely on the existing authorization policy model.

## 5. HTTP contract

`POST /api/v1/library/imports` supports:

- `application/json` with the existing `markdown` field, interpreted as Markdown unless `format: text` is supplied.
- `multipart/form-data` with metadata fields and one `source` file.

The upload limit is enforced before multipart parsing. The handler does not trust filename extensions alone: PDF and EPUB adapters validate their signatures/containers. The response remains the existing import-job envelope, extended with the imported source format.

## 6. Dashboard behavior

The file input accepts all supported local formats. Selecting Markdown/text loads editable text for compatibility; selecting PDF/EPUB retains the `File` object and displays filename, format, and size without decoding bytes into component text. The API client constructs `FormData` and lets the browser set the multipart boundary.

Reader and library IDs are omitted from the dashboard form and its requests. Library type remains user-selectable because it changes authorization semantics, and organization ID remains visible only for organization libraries.

Book language is selected from a fixed dashboard list of commonly used BCP 47-style tags. English is the default. Labels include native writing systems so the control itself remains understandable for non-English books. The selected tag continues through the existing `language` transport field; extraction retains Unicode text unchanged and does not reinterpret the tag as a legacy character encoding.

## 7. Failure modes

- Unsupported extension or format: reject before persistence.
- Malformed EPUB/PDF: failed job response with parser context.
- Encrypted or unsupported PDF: fail explicitly; do not silently import partial text.
- Native-text PDF with some scanned pages: import native passages and retain OCR-required page nodes.
- Entirely scanned PDF: reject as requiring configured OCR because it has no searchable native passages.
- Oversized upload: return HTTP 413.
- Duplicate bytes: return the existing edition/asset counts and refresh source policy.
- Missing workspace: reject before identity derivation because no stable local scope exists.
- Missing organization ID for an organization library: reject rather than generating a shared-library identity from incomplete scope.
- Unsupported dashboard language: cannot be entered through the selector; explicit API clients remain responsible for sending a valid non-empty language tag.

## 8. Security, performance, and rollout

The request limit bounds memory/disk exposure. Multipart temporary files are removed after parsing. Parsing stays local and does not send copyrighted source bytes to hosted services. PDF pages and EPUB spine items are processed in source order; indexing remains synchronous for this slice but preserves the job response contract for later background execution.

The new path is additive. Rollback removes multipart handling and format registration while leaving existing imported editions readable. Parser versions on assets and locators support later re-indexing without changing durable citations silently.

Identity derivation is deterministic and stateless, so it adds no migration, lookup, or coordination cost. The generated values are opaque locators, not credentials. A rollback can restore explicit dashboard fields while all generated records remain addressable through the same IDs.

## 9. Alternatives

Requiring Poppler was rejected because installation must work across client devices without a separately managed executable. Browser-side PDF parsing was rejected because it would duplicate backend provenance logic and make API/CLI imports inconsistent. Treating every binary as plain text was rejected because it destroys reading order and citation locators.

Browser-generated random IDs were rejected because browser storage is origin-specific and can change with the dashboard address. Persisting a new installation identity in every workspace database was rejected for this local single-reader flow because it would require a migration and cross-workspace coordination. Deterministic server-side IDs preserve stable behavior without new state.

## 10. Local inference provider contract

The optional provider uses the OpenAI-compatible HTTP surface rather than an Ollama-specific API. The minimum setup fields are enabled state, loopback base URL, text model, optional vision model, optional API key, and bounded request timeout. The configuration store belongs to the backend data directory, uses owner-only permissions, and never returns the credential value.

Connectivity validation calls the provider model-list endpoint with a short timeout and reports four independent facts: whether configuration exists, whether it is enabled, whether the endpoint is reachable, and whether the configured text model is advertised. Reachability is diagnostic, not proof that a book was processed. Import jobs must later record the processing mode and model/version actually used before any generated artifact can claim local-model provenance.

Only loopback HTTP endpoints are accepted in the initial local-only rollout. This prevents the dashboard from becoming an arbitrary server-side request primitive. Supporting remote private-network endpoints requires a later explicit allowlist and transport-security design.

## 11. Import decision flow

```mermaid
flowchart TD
    Select["Reader selects a complete book"] --> Status["Load local inference status"]
    Status --> Ready{"Endpoint enabled and reachable?"}
    Ready -->|"Yes"| Baseline["Run deterministic format parser"]
    Ready -->|"No"| Decision{"Reader decision"}
    Decision -->|"Set up"| Setup["Configure and test OpenAI-compatible endpoint"]
    Decision -->|"Use parser"| Baseline
    Decision -->|"Cancel"| Stop["Leave source unmodified"]
    Baseline --> Index["Persist source structure passages and locators"]
    Index --> Future["Optional observable enrichment task"]
```

The decision gate appears only when no remembered parser-only choice exists and the endpoint is not operational. Remembering parser-only mode is a device-local convenience, not an authorization or source-retention decision. It can be reset from the import card.

## 12. Failure modes, performance, and rollout

- Missing configuration: return a stable disabled status without network traffic.
- Malformed or non-loopback URL: reject before persistence or connection attempts.
- Provider timeout, refusal, invalid JSON, or missing model: retain configuration state, report an actionable diagnostic, and keep parser-only import available.
- Credential update with an empty API-key field: preserve the existing secret; explicit credential removal requires a dedicated clear operation.
- Concurrent dashboard requests: serialize atomic configuration replacement and write through a temporary file before rename.
- Status latency: use a short bounded timeout and allow the UI to display saved configuration before connectivity resolves.
- Rollout: ship configuration/status and the parser decision gate first; add text enrichment next; add rendered-page vision/OCR only after locator reconciliation and OCR verification are specified.

The main alternative was browser-owned configuration. It was rejected because storing credentials in local storage exposes them to dashboard JavaScript and prevents CLI/background jobs from sharing the provider. Reusing embedding configuration was rejected because embedding and generative/vision models have different contracts, lifecycle, and security boundaries.
