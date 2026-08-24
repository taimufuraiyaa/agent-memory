package library

import (
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"strings"
	"time"
)

type ReadingState string

const (
	ReadingSeen      ReadingState = "seen"
	ReadingStudied   ReadingState = "studied"
	ReadingMastered  ReadingState = "mastered"
	ReadingCompleted ReadingState = "completed"
)

type ReadingProgress struct {
	PrincipalID string             `json:"principal_id"`
	EditionID   string             `json:"edition_id"`
	State       ReadingState       `json:"state"`
	Locator     core.SourceLocator `json:"locator"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

func (p ReadingProgress) Validate() error {
	if strings.TrimSpace(p.PrincipalID) == "" || strings.TrimSpace(p.EditionID) == "" || p.UpdatedAt.IsZero() {
		return errors.New("reading progress principal, edition, and update time are required")
	}
	switch p.State {
	case ReadingSeen, ReadingStudied, ReadingMastered, ReadingCompleted:
	default:
		return errors.New("invalid reading state")
	}
	return p.Locator.Validate()
}
