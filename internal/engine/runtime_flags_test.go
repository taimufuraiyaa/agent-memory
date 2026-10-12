package engine

import "testing"

func TestLibraryRecoveryGateIsEnabledByDefaultWithEmergencyDisable(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LIBRARY_ENABLED", "")
	if !LibraryEnabled() {
		t.Fatal("library should be enabled after I4 drills")
	}
	t.Setenv("AGENT_MEMORY_LIBRARY_ENABLED", "false")
	if LibraryEnabled() {
		t.Fatal("emergency disable flag ignored")
	}
}

func TestLexicalAndPassageRankingModesAreIndependentAndDefaultOff(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LEXICAL_MODE", "")
	t.Setenv("AGENT_MEMORY_PASSAGE_RANKING_MODE", "")
	if LexicalMode() != LexicalOff || PassageRankingMode() != PassageRankingOff {
		t.Fatalf("defaults = lexical %q passage %q, want both off", LexicalMode(), PassageRankingMode())
	}

	t.Setenv("AGENT_MEMORY_LEXICAL_MODE", "on")
	if LexicalMode() != LexicalOn || PassageRankingMode() != PassageRankingOff {
		t.Fatalf("lexical on must not enable passage ranking: lexical %q passage %q", LexicalMode(), PassageRankingMode())
	}

	t.Setenv("AGENT_MEMORY_LEXICAL_MODE", "off")
	t.Setenv("AGENT_MEMORY_PASSAGE_RANKING_MODE", " ON ")
	if LexicalMode() != LexicalOff || PassageRankingMode() != PassageRankingOn {
		t.Fatalf("passage on must not enable lexical stream: lexical %q passage %q", LexicalMode(), PassageRankingMode())
	}

	t.Setenv("AGENT_MEMORY_PASSAGE_RANKING_MODE", "bogus")
	if PassageRankingMode() != PassageRankingOff {
		t.Fatal("unknown passage ranking value must fall back to off")
	}
}
