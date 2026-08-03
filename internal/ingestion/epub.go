package ingestion

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"io"
	"path"
	"strings"
)

type EPUBMetadata struct {
	Title      string `json:"title"`
	Language   string `json:"language"`
	Identifier string `json:"identifier"`
}
type EPUBExtraction struct {
	Metadata EPUBMetadata      `json:"metadata"`
	Spine    []string          `json:"spine"`
	Document ExtractedDocument `json:"document"`
	Passages []library.Passage `json:"passages"`
}
type EPUBAdapter struct{ ParserVersion, NormalizationVersion string }

func (a EPUBAdapter) Extract(editionID, assetID string, source []byte) (EPUBExtraction, error) {
	if editionID == "" || assetID == "" || a.ParserVersion == "" || a.NormalizationVersion == "" {
		return EPUBExtraction{}, errors.New("epub adapter identity and versions are required")
	}
	reader, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		return EPUBExtraction{}, err
	}
	files := map[string]*zip.File{}
	for _, f := range reader.File {
		files[path.Clean(f.Name)] = f
	}
	container, ok := files["META-INF/container.xml"]
	if !ok {
		return EPUBExtraction{}, errors.New("epub container is missing")
	}
	containerBytes, err := readZip(container)
	if err != nil {
		return EPUBExtraction{}, err
	}
	var c struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(containerBytes, &c); err != nil || len(c.Rootfiles) == 0 {
		return EPUBExtraction{}, errors.New("invalid epub container")
	}
	opfPath := path.Clean(c.Rootfiles[0].FullPath)
	opfFile, ok := files[opfPath]
	if !ok {
		return EPUBExtraction{}, errors.New("epub package is missing")
	}
	opfBytes, err := readZip(opfFile)
	if err != nil {
		return EPUBExtraction{}, err
	}
	var pkg struct {
		Metadata struct {
			Title      string `xml:"title"`
			Language   string `xml:"language"`
			Identifier string `xml:"identifier"`
		} `xml:"metadata"`
		Manifest []struct {
			ID   string `xml:"id,attr"`
			Href string `xml:"href,attr"`
		} `xml:"manifest>item"`
		Spine []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"spine>itemref"`
	}
	if err := xml.Unmarshal(opfBytes, &pkg); err != nil {
		return EPUBExtraction{}, fmt.Errorf("invalid epub package: %w", err)
	}
	manifest := map[string]string{}
	for _, item := range pkg.Manifest {
		manifest[item.ID] = path.Join(path.Dir(opfPath), item.Href)
	}
	out := EPUBExtraction{Metadata: EPUBMetadata{Title: strings.TrimSpace(pkg.Metadata.Title), Language: strings.TrimSpace(pkg.Metadata.Language), Identifier: strings.TrimSpace(pkg.Metadata.Identifier)}, Document: ExtractedDocument{EditionID: editionID, ParserVersion: a.ParserVersion, NormalizationVersion: a.NormalizationVersion}}
	offset := 0
	for ordinal, item := range pkg.Spine {
		filePath, exists := manifest[item.IDRef]
		if !exists {
			return EPUBExtraction{}, fmt.Errorf("spine item %s is missing", item.IDRef)
		}
		f, exists := files[path.Clean(filePath)]
		if !exists {
			return EPUBExtraction{}, fmt.Errorf("spine content %s is missing", filePath)
		}
		content, err := readZip(f)
		if err != nil {
			return EPUBExtraction{}, err
		}
		text, title, err := xhtmlText(content)
		if err != nil {
			return EPUBExtraction{}, fmt.Errorf("invalid spine xhtml: %w", err)
		}
		if title == "" {
			title = item.IDRef
		}
		nodeID := stableNodeID(editionID, ordinal, title)
		end := offset + len(text)
		node := library.StructuralNode{ID: nodeID, EditionID: editionID, Kind: library.NodeChapter, Ordinal: ordinal, Title: title, StartOffset: offset, EndOffset: end, Explicit: true}
		section := ExtractedSection{NodeID: nodeID, SourceText: text, NormalizedText: text, Span: SourceSpan{SourceStart: 0, SourceEnd: len(content), NormalizedStart: offset, NormalizedEnd: end}}
		passageID := stableImportID("passage", editionID, assetID, filePath, text)
		passage := library.Passage{ID: passageID, EditionID: editionID, SourceAssetID: assetID, StructuralNodeID: nodeID, Text: text, Fingerprint: core.FingerprintText(text), Locator: core.SourceLocator{Kind: core.LocatorEPUB, Display: title, ParserVersion: a.ParserVersion, NormalizationVersion: a.NormalizationVersion, EPUB: &core.EPUBLocator{SpineItem: filePath, CFI: fmt.Sprintf("epubcfi(/6/%d)", (ordinal+1)*2)}}}
		out.Spine = append(out.Spine, filePath)
		out.Document.Nodes = append(out.Document.Nodes, node)
		out.Document.Sections = append(out.Document.Sections, section)
		out.Passages = append(out.Passages, passage)
		if out.Document.NormalizedText != "" {
			out.Document.NormalizedText += "\n\n"
		}
		out.Document.NormalizedText += text
		offset = end + 2
	}
	if len(out.Spine) == 0 {
		return EPUBExtraction{}, errors.New("epub spine is empty")
	}
	return out, nil
}
func readZip(f *zip.File) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
func xhtmlText(data []byte) (string, string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	words := []string{}
	title := ""
	captureTitle := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", err
		}
		switch v := token.(type) {
		case xml.StartElement:
			if v.Name.Local == "h1" || v.Name.Local == "title" {
				captureTitle = true
			}
		case xml.EndElement:
			if v.Name.Local == "h1" || v.Name.Local == "title" {
				captureTitle = false
			}
		case xml.CharData:
			value := strings.Join(strings.Fields(string(v)), " ")
			if value != "" {
				words = append(words, value)
				if captureTitle && title == "" {
					title = value
				}
			}
		}
	}
	return strings.Join(words, " "), title, nil
}
