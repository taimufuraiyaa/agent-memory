# Library parser boundaries

The library core does not depend on a hosted parser or model provider.

| Format | Implemented boundary | Version/provenance rule |
|---|---|---|
| Markdown/text | Native deterministic heading parser | `markdown-v1` + normalization version |
| EPUB | Go standard-library ZIP/XML package, OPF manifest/spine and XHTML token extraction | Parser version stored on assets and locators |
| PDF | Page/block `PDFPageExtractor` boundary with deterministic page, column and coordinate handling | Concrete extractor identifies its parser version; scanned pages become `requires_ocr` |
| PDF OCR | Configurable `OCRProvider` returning provider, version and confidence | OCR stays in a separate layer; low-confidence text cannot auto-verify quotes |
| Web | Injected fetcher and robots checker with canonical URL and immutable capture fingerprint | Every changed capture gets a new capture ID |

No production PDF or OCR vendor is selected by this repository. Deployments must
configure those interfaces and record their versions; the fixture adapters test the
contract without sending book content to a third party.
