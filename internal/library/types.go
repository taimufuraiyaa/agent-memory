// Package library defines books, editions, source assets, structures, and citations.
package library

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type BookWork struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	NormalizedTitle string `json:"normalized_title"`
}

func (w BookWork) Validate() error {
	if strings.TrimSpace(w.ID) == "" || strings.TrimSpace(w.Title) == "" || strings.TrimSpace(w.NormalizedTitle) == "" {
		return errors.New("book work requires id, title, and normalized title")
	}
	return nil
}

type BookEdition struct {
	ID                 string `json:"id"`
	WorkID             string `json:"work_id"`
	Label              string `json:"label"`
	Language           string `json:"language"`
	ContentFingerprint string `json:"content_fingerprint"`
}

func (e BookEdition) Validate() error {
	if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.WorkID) == "" || e.ID == e.WorkID {
		return errors.New("edition requires distinct id and work id")
	}
	if strings.TrimSpace(e.Label) == "" || strings.TrimSpace(e.Language) == "" || strings.TrimSpace(e.ContentFingerprint) == "" {
		return errors.New("edition requires label, language, and content fingerprint")
	}
	return nil
}

type SourceFormat string

const (
	FormatPDF      SourceFormat = "pdf"
	FormatEPUB     SourceFormat = "epub"
	FormatMarkdown SourceFormat = "markdown"
	FormatText     SourceFormat = "text"
	FormatWeb      SourceFormat = "web"
)

type SourceAsset struct {
	ID                    string            `json:"id"`
	EditionID             string            `json:"edition_id"`
	Format                SourceFormat      `json:"format"`
	ByteFingerprint       string            `json:"byte_fingerprint"`
	NormalizedFingerprint string            `json:"normalized_fingerprint"`
	ParserVersion         string            `json:"parser_version"`
	Policy                core.SourcePolicy `json:"policy"`
	ImportedAt            time.Time         `json:"imported_at"`
}

func (a SourceAsset) Validate() error {
	if strings.TrimSpace(a.ID) == "" || strings.TrimSpace(a.EditionID) == "" || strings.TrimSpace(a.ByteFingerprint) == "" ||
		strings.TrimSpace(a.NormalizedFingerprint) == "" || strings.TrimSpace(a.ParserVersion) == "" || a.ImportedAt.IsZero() {
		return errors.New("source asset identity, fingerprints, parser version, and import time are required")
	}
	switch a.Format {
	case FormatPDF, FormatEPUB, FormatMarkdown, FormatText, FormatWeb:
	default:
		return errors.New("invalid source format")
	}
	return a.Policy.Validate()
}

type StructuralNodeKind string

const (
	NodePart       StructuralNodeKind = "part"
	NodeChapter    StructuralNodeKind = "chapter"
	NodeSection    StructuralNodeKind = "section"
	NodeSubsection StructuralNodeKind = "subsection"
	NodeAppendix   StructuralNodeKind = "appendix"
)

type StructuralNode struct {
	ID          string             `json:"id"`
	EditionID   string             `json:"edition_id"`
	ParentID    *string            `json:"parent_id,omitempty"`
	Kind        StructuralNodeKind `json:"kind"`
	Ordinal     int                `json:"ordinal"`
	Title       string             `json:"title"`
	StartOffset int                `json:"start_offset,omitempty"`
	EndOffset   int                `json:"end_offset,omitempty"`
	Explicit    bool               `json:"explicit"`
}

func (n StructuralNode) Validate() error {
	if strings.TrimSpace(n.ID) == "" || strings.TrimSpace(n.EditionID) == "" || strings.TrimSpace(n.Title) == "" || n.Ordinal < 0 {
		return errors.New("structural node requires identity, title, and non-negative ordinal")
	}
	switch n.Kind {
	case NodePart, NodeChapter, NodeSection, NodeSubsection, NodeAppendix:
	default:
		return errors.New("invalid structural node kind")
	}
	if n.StartOffset < 0 || n.EndOffset < 0 || (n.EndOffset > 0 && n.EndOffset <= n.StartOffset) {
		return errors.New("invalid structural node offsets")
	}
	return nil
}

func ValidateStructure(nodes []StructuralNode) error {
	byID := make(map[string]StructuralNode, len(nodes))
	ordinals := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if err := node.Validate(); err != nil {
			return err
		}
		if _, exists := byID[node.ID]; exists {
			return errors.New("duplicate structural node id")
		}
		byID[node.ID] = node
		parent := "root"
		if node.ParentID != nil {
			parent = *node.ParentID
		}
		ordinalKey := fmt.Sprintf("%s:%s:%d", parent, node.Kind, node.Ordinal)
		if _, exists := ordinals[ordinalKey]; exists {
			return errors.New("duplicate sibling ordinal")
		}
		ordinals[ordinalKey] = struct{}{}
	}
	for _, node := range nodes {
		seen := map[string]bool{node.ID: true}
		current := node
		for current.ParentID != nil {
			parent, ok := byID[*current.ParentID]
			if !ok || parent.EditionID != node.EditionID {
				return errors.New("structural parent does not exist in edition")
			}
			if seen[parent.ID] {
				return errors.New("structural hierarchy contains cycle")
			}
			seen[parent.ID] = true
			current = parent
		}
	}
	return nil
}
