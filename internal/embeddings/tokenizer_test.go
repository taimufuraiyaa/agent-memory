package embeddings

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestWordPieceTokenizerEncode(t *testing.T) {
	modelDir := filepath.Join(t.TempDir(), "all-MiniLM-L6-v2")
	writeMiniLMTestTokenizer(t, modelDir)

	tokenizer, err := LoadWordPieceTokenizer(modelDir)
	if err != nil {
		t.Fatalf("load tokenizer: %v", err)
	}

	got, err := tokenizer.Encode("Hello worlds!")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	wantTokens := []string{"[CLS]", "hello", "world", "##s", "!", "[SEP]"}
	if !reflect.DeepEqual(got.Tokens, wantTokens) {
		t.Fatalf("unexpected tokens: got %v want %v", got.Tokens, wantTokens)
	}

	wantIDs := []int64{101, 2001, 2002, 2003, 2004, 102, 0, 0}
	if !reflect.DeepEqual(got.InputIDs, wantIDs) {
		t.Fatalf("unexpected input ids: got %v want %v", got.InputIDs, wantIDs)
	}

	wantMask := []int64{1, 1, 1, 1, 1, 1, 0, 0}
	if !reflect.DeepEqual(got.AttentionMask, wantMask) {
		t.Fatalf("unexpected attention mask: got %v want %v", got.AttentionMask, wantMask)
	}
}

func TestWordPieceTokenizerTruncatesAndPads(t *testing.T) {
	modelDir := filepath.Join(t.TempDir(), "all-MiniLM-L6-v2")
	writeMiniLMTestTokenizer(t, modelDir)

	tokenizer, err := LoadWordPieceTokenizer(modelDir)
	if err != nil {
		t.Fatalf("load tokenizer: %v", err)
	}

	got, err := tokenizer.Encode("hello world service hello world service hello")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	if len(got.InputIDs) != 8 || len(got.AttentionMask) != 8 || len(got.TokenTypeIDs) != 8 {
		t.Fatalf("expected fixed size 8, got ids=%d mask=%d types=%d", len(got.InputIDs), len(got.AttentionMask), len(got.TokenTypeIDs))
	}
	if got.Tokens[0] != "[CLS]" || got.Tokens[len(got.Tokens)-1] != "[SEP]" {
		t.Fatalf("expected special tokens around truncated sequence, got %v", got.Tokens)
	}
}

func writeMiniLMTestTokenizer(t *testing.T, modelDir string) {
	t.Helper()
	writeMiniLMTestModelFiles(t, modelDir, `{
  "truncation": {"max_length": 8},
  "normalizer": {
    "type": "BertNormalizer",
    "clean_text": true,
    "handle_chinese_chars": true,
    "strip_accents": null,
    "lowercase": true
  },
  "model": {
    "type": "WordPiece",
    "unk_token": "[UNK]",
    "continuing_subword_prefix": "##",
    "max_input_chars_per_word": 100,
    "vocab": {
      "[PAD]": 0,
      "[UNK]": 100,
      "[CLS]": 101,
      "[SEP]": 102,
      "hello": 2001,
      "world": 2002,
      "##s": 2003,
      "!": 2004,
      "service": 2005,
      "cafe": 2006
    }
  }
}`)
}
