# Library parser boundaries

The library core does not depend on a hosted parser or model provider.

| Format | Implemented boundary | Version/provenance rule |
|---|---|---|
| Markdown/text | Native deterministic heading parser | `markdown-v1` + normalization version |
| EPUB | Go standard-library ZIP/XML package, OPF manifest/spine and XHTML token extraction | Parser version stored on assets and locators |
| PDF | In-process `github.com/ledongthuc/pdf` native-text extractor behind the `PDFPageExtractor` boundary, with deterministic page, column and coordinate handling | `pdf-ledongthuc-5959a402` is stored on assets and locators; scanned pages become `requires_ocr` |
| PDF OCR | Configurable `OCRProvider` returning provider, version and confidence | OCR stays in a separate layer; low-confidence text cannot auto-verify quotes |
| Web | Injected fetcher and robots checker with canonical URL and immutable capture fingerprint | Every changed capture gets a new capture ID |

The default PDF extractor runs locally inside the agent-memory process and does not
send book content to a third party. No production OCR provider is selected by this
repository. Deployments that enable OCR must configure that interface and record
its provider and version; low-confidence OCR remains ineligible for automatic quote
verification.
