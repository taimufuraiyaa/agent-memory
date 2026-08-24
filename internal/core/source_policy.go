package core

import "errors"

type RetentionMode string

const (
	RetentionRetained    RetentionMode = "retained"
	RetentionOnDemand    RetentionMode = "on_demand"
	RetentionSessionOnly RetentionMode = "session_only"
	RetentionDeleted     RetentionMode = "deleted"
)

type SourcePolicy struct {
	Retention       RetentionMode `json:"retention"`
	StoreOriginal   bool          `json:"store_original"`
	StoreNormalized bool          `json:"store_normalized"`
	AllowSearch     bool          `json:"allow_search"`
	AllowQuote      bool          `json:"allow_quote"`
	AllowShare      bool          `json:"allow_share"`
	AllowExport     bool          `json:"allow_export"`
	MaxQuoteRunes   int           `json:"max_quote_runes,omitempty"`
}

func (p SourcePolicy) Validate() error {
	switch p.Retention {
	case RetentionRetained, RetentionOnDemand, RetentionSessionOnly, RetentionDeleted:
	default:
		return errors.New("invalid source retention mode")
	}
	if p.MaxQuoteRunes < 0 {
		return errors.New("quote limit cannot be negative")
	}
	if p.AllowQuote && p.MaxQuoteRunes == 0 {
		return errors.New("quoting requires a positive quote limit")
	}
	if p.Retention == RetentionSessionOnly && (p.StoreOriginal || p.StoreNormalized || p.AllowShare || p.AllowExport) {
		return errors.New("session-only source cannot be persisted, shared, or exported")
	}
	if p.Retention == RetentionDeleted && (p.StoreOriginal || p.StoreNormalized || p.AllowSearch || p.AllowQuote || p.AllowShare || p.AllowExport) {
		return errors.New("deleted source cannot retain storage or use permissions")
	}
	return nil
}

func (p SourcePolicy) CanQuote(text string) bool {
	return p.Validate() == nil && p.AllowQuote && len([]rune(text)) <= p.MaxQuoteRunes
}

func (p SourcePolicy) CanShare() bool {
	return p.Validate() == nil && p.AllowShare
}
