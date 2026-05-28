package embeddings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const defaultMiniLMSequenceLength = 128

// TokenizedInput is the padded single-sequence input expected by the MiniLM model.
type TokenizedInput struct {
	InputIDs      []int64
	AttentionMask []int64
	TokenTypeIDs  []int64
	Tokens        []string
}

// WordPieceTokenizer loads and applies the Bert-style tokenizer used by MiniLM.
type WordPieceTokenizer struct {
	vocab                 map[string]int64
	unkToken              string
	clsToken              string
	sepToken              string
	padToken              string
	maxSequenceLength     int
	maxInputCharsPerWord  int
	continuingSubwordPref string
	lowercase             bool
	cleanText             bool
	handleChineseChars    bool
	stripAccents          bool
}

type tokenizerConfig struct {
	Truncation struct {
		MaxLength int `json:"max_length"`
	} `json:"truncation"`
	Normalizer struct {
		Type               string `json:"type"`
		CleanText          bool   `json:"clean_text"`
		HandleChineseChars bool   `json:"handle_chinese_chars"`
		StripAccents       *bool  `json:"strip_accents"`
		Lowercase          bool   `json:"lowercase"`
	} `json:"normalizer"`
	Model struct {
		Type                  string         `json:"type"`
		UnkToken              string         `json:"unk_token"`
		ContinuingSubwordPref string         `json:"continuing_subword_prefix"`
		MaxInputCharsPerWord  int            `json:"max_input_chars_per_word"`
		Vocab                 map[string]int `json:"vocab"`
	} `json:"model"`
}

// LoadWordPieceTokenizer loads the MiniLM tokenizer from tokenizer.json.
func LoadWordPieceTokenizer(modelDir string) (*WordPieceTokenizer, error) {
	if strings.TrimSpace(modelDir) == "" {
		return nil, errors.New("model dir is required")
	}
	path := filepath.Join(modelDir, "tokenizer.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tokenizer.json: %w", err)
	}

	var cfg tokenizerConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse tokenizer.json: %w", err)
	}
	if !strings.EqualFold(cfg.Model.Type, "WordPiece") {
		return nil, fmt.Errorf("unsupported tokenizer model type: %s", cfg.Model.Type)
	}
	if len(cfg.Model.Vocab) == 0 {
		return nil, errors.New("tokenizer vocab is empty")
	}

	vocab := make(map[string]int64, len(cfg.Model.Vocab))
	for token, id := range cfg.Model.Vocab {
		vocab[token] = int64(id)
	}

	maxSeqLen := cfg.Truncation.MaxLength
	if maxSeqLen <= 0 {
		maxSeqLen = defaultMiniLMSequenceLength
	}
	maxInputCharsPerWord := cfg.Model.MaxInputCharsPerWord
	if maxInputCharsPerWord <= 0 {
		maxInputCharsPerWord = 100
	}
	continuingPrefix := cfg.Model.ContinuingSubwordPref
	if continuingPrefix == "" {
		continuingPrefix = "##"
	}

	stripAccents := cfg.Normalizer.Lowercase
	if cfg.Normalizer.StripAccents != nil {
		stripAccents = *cfg.Normalizer.StripAccents
	}

	t := &WordPieceTokenizer{
		vocab:                 vocab,
		unkToken:              firstNonEmpty(cfg.Model.UnkToken, "[UNK]"),
		clsToken:              "[CLS]",
		sepToken:              "[SEP]",
		padToken:              "[PAD]",
		maxSequenceLength:     maxSeqLen,
		maxInputCharsPerWord:  maxInputCharsPerWord,
		continuingSubwordPref: continuingPrefix,
		lowercase:             cfg.Normalizer.Lowercase,
		cleanText:             cfg.Normalizer.CleanText,
		handleChineseChars:    cfg.Normalizer.HandleChineseChars,
		stripAccents:          stripAccents,
	}
	for _, special := range []string{t.unkToken, t.clsToken, t.sepToken, t.padToken} {
		if _, ok := t.vocab[special]; !ok {
			return nil, fmt.Errorf("tokenizer vocab missing required token: %s", special)
		}
	}
	return t, nil
}

// Encode converts a single string to fixed-length model inputs.
func (t *WordPieceTokenizer) Encode(text string) (TokenizedInput, error) {
	if t == nil {
		return TokenizedInput{}, errors.New("tokenizer is required")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return TokenizedInput{}, errors.New("text is required")
	}

	normalized := t.normalize(text)
	pieces := make([]string, 0, 32)
	for _, token := range bertPreTokenize(normalized) {
		pieces = append(pieces, t.wordPiece(token)...)
	}

	usable := t.maxSequenceLength - 2
	if usable < 0 {
		return TokenizedInput{}, fmt.Errorf("invalid tokenizer max sequence length: %d", t.maxSequenceLength)
	}
	if len(pieces) > usable {
		pieces = pieces[:usable]
	}

	tokens := make([]string, 0, t.maxSequenceLength)
	tokens = append(tokens, t.clsToken)
	tokens = append(tokens, pieces...)
	tokens = append(tokens, t.sepToken)

	inputIDs := make([]int64, 0, t.maxSequenceLength)
	attentionMask := make([]int64, 0, t.maxSequenceLength)
	tokenTypeIDs := make([]int64, 0, t.maxSequenceLength)
	for _, token := range tokens {
		id, ok := t.vocab[token]
		if !ok {
			id = t.vocab[t.unkToken]
		}
		inputIDs = append(inputIDs, id)
		attentionMask = append(attentionMask, 1)
		tokenTypeIDs = append(tokenTypeIDs, 0)
	}

	padID := t.vocab[t.padToken]
	for len(inputIDs) < t.maxSequenceLength {
		inputIDs = append(inputIDs, padID)
		attentionMask = append(attentionMask, 0)
		tokenTypeIDs = append(tokenTypeIDs, 0)
	}

	return TokenizedInput{
		InputIDs:      inputIDs,
		AttentionMask: attentionMask,
		TokenTypeIDs:  tokenTypeIDs,
		Tokens:        tokens,
	}, nil
}

func (t *WordPieceTokenizer) normalize(text string) string {
	if t.cleanText {
		text = cleanBERTText(text)
	}
	if t.handleChineseChars {
		text = spaceChineseChars(text)
	}
	if t.lowercase {
		text = strings.ToLower(text)
	}
	if t.stripAccents {
		text = stripAccents(text)
	}
	return strings.TrimSpace(text)
}

func (t *WordPieceTokenizer) wordPiece(token string) []string {
	if token == "" {
		return nil
	}
	if id, ok := t.vocab[token]; ok && id >= 0 {
		return []string{token}
	}
	runes := []rune(token)
	if len(runes) > t.maxInputCharsPerWord {
		return []string{t.unkToken}
	}

	out := make([]string, 0, len(runes))
	for start := 0; start < len(runes); {
		end := len(runes)
		match := ""
		for start < end {
			piece := string(runes[start:end])
			if start > 0 {
				piece = t.continuingSubwordPref + piece
			}
			if _, ok := t.vocab[piece]; ok {
				match = piece
				break
			}
			end--
		}
		if match == "" {
			return []string{t.unkToken}
		}
		out = append(out, match)
		start = end
	}
	return out
}

func cleanBERTText(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case r == 0 || r == unicode.ReplacementChar:
			continue
		case isControlRune(r):
			continue
		case isWhitespaceRune(r):
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func spaceChineseChars(text string) string {
	var b strings.Builder
	b.Grow(len(text) * 2)
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			b.WriteByte(' ')
			b.WriteRune(r)
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func stripAccents(text string) string {
	decomposed := norm.NFD.String(text)
	var b strings.Builder
	b.Grow(len(decomposed))
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func bertPreTokenize(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	fields := strings.Fields(text)
	out := make([]string, 0, len(fields)*2)
	for _, field := range fields {
		var current []rune
		flush := func() {
			if len(current) == 0 {
				return
			}
			out = append(out, string(current))
			current = current[:0]
		}
		for _, r := range field {
			if isPunctuationRune(r) {
				flush()
				out = append(out, string(r))
				continue
			}
			current = append(current, r)
		}
		flush()
	}
	return out
}

func isPunctuationRune(r rune) bool {
	if unicode.IsPunct(r) {
		return true
	}
	if r >= '!' && r <= '/' {
		return true
	}
	if r >= ':' && r <= '@' {
		return true
	}
	if r >= '[' && r <= '`' {
		return true
	}
	return r >= '{' && r <= '~'
}

func isWhitespaceRune(r rune) bool {
	return unicode.IsSpace(r)
}

func isControlRune(r rune) bool {
	if r == '\t' || r == '\n' || r == '\r' {
		return false
	}
	return unicode.IsControl(r)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
