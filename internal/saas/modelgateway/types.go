package modelgateway

import (
	"context"
	"errors"
	"time"
)

var (
	ErrProviderPolicy = errors.New("model provider policy denied the request")
	ErrCircuitOpen    = errors.New("model provider circuit is open")
)

type Provider interface {
	Name() string
	ModelVersion() string
	RetentionPolicy() string
	Dimension() int
	EmbedBatch(context.Context, []string) ([][]float32, error)
	Generate(context.Context, string) (string, error)
}

type Redactor interface{ Redact(string) string }
type UsageSink interface {
	RecordUsage(context.Context, Usage) error
}
type QuotaChecker interface {
	AllowModel(context.Context, string, int, time.Time) (bool, error)
}

type ProviderPolicy struct {
	Provider             string
	Models               []string
	RetentionPolicies    []string
	MaxInputTokens       int
	Timeout              time.Duration
	MaxRetries           int
	FailureThreshold     int
	Cooldown             time.Duration
	InputCostPerMillion  int64
	OutputCostPerMillion int64
}

type Config struct {
	Providers []Provider
	Policies  []ProviderPolicy
	Quota     QuotaChecker
}

type EmbedRequest struct {
	TenantID      string
	SourceID      string
	SourceVersion int64
	Provider      string
	Model         string
	Texts         []string
}

type EmbedResponse struct {
	Provider   string
	Model      string
	Dimensions int
	Vectors    [][]float32
}

type Evidence struct {
	SourceID  string `json:"source_id"`
	PassageID string `json:"passage_id"`
	Text      string `json:"text"`
}

type GenerateRequest struct {
	TenantID string
	Provider string
	Model    string
	Prompt   string
	Evidence []Evidence
}

type GenerateResponse struct {
	Text        string
	Evidence    []Evidence
	Generated   bool
	FailureCode string
}

type Usage struct {
	TenantID            string
	SourceID            string
	SourceVersion       int64
	Operation           string
	Provider            string
	Model               string
	Dimensions          int
	InputTokens         int
	OutputTokens        int
	EstimatedCostMicros int64
	Outcome             string
	OccurredAt          time.Time
}

type temporaryError struct{ error }

func (temporaryError) Temporary() bool { return true }
func Temporary(err error) error {
	if err == nil {
		return nil
	}
	return temporaryError{error: err}
}

type temporary interface{ Temporary() bool }
