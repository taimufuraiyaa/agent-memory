package core

import "time"

type AdaptivePolicyDefaults struct {
	MinSemanticScore    float64
	MinTotalScore       float64
	RelativeScoreCutoff float64
	WeakSemanticScore   float64
	WeakTotalScore      float64
	WeakRelativeCutoff  float64
}

type AdaptiveSignalTuning struct {
	SalienceScoreFactor       float64
	UsefulCountStep           float64
	UsefulCountCap            float64
	LastHelpfulRecencyWeight  float64
	SuppressionScoreFactor    float64
	RejectedCountStep         float64
	RejectedCountCap          float64
	HarmfulCountStep          float64
	HarmfulCountCap           float64
	LastRejectedRecencyWeight float64
	ActiveSuppressionBoost    float64
	PinnedSuppressionFactor   float64
	SuppressionBandThreshold  float64
}

type AdaptiveFeedbackTuning struct {
	HelpfulSalienceDelta             float64
	HelpfulSuppressionDelta          float64
	IgnoredSalienceDelta             float64
	IgnoredSuppressionDelta          float64
	RejectedSalienceDelta            float64
	RejectedSuppressionDelta         float64
	HarmfulSalienceDelta             float64
	HarmfulSuppressionDelta          float64
	ConfirmedSalienceDelta           float64
	ClarifiedSalienceDelta           float64
	ContradictedSalienceDelta        float64
	ContradictedSuppressionDelta     float64
	SupersededSalienceDelta          float64
	SupersededSuppressionDelta       float64
	RejectedCooldown                 time.Duration
	HarmfulCooldown                  time.Duration
	ContradictedCooldown             time.Duration
}

func DefaultAdaptivePolicy(mode string) AdaptivePolicyDefaults {
	base := AdaptivePolicyDefaults{
		MinSemanticScore:    0.02,
		MinTotalScore:       0.02,
		RelativeScoreCutoff: 0.01,
		WeakSemanticScore:   0,
		WeakTotalScore:      0,
		WeakRelativeCutoff:  0,
	}
	switch mode {
	case "recall":
		base.MinSemanticScore = 0.03
		base.MinTotalScore = 0.03
		base.RelativeScoreCutoff = 0.02
		base.WeakSemanticScore = 0.01
		base.WeakTotalScore = 0.01
		base.WeakRelativeCutoff = 0.01
	case "relate":
		base.MinSemanticScore = 0.2
		base.MinTotalScore = 0.17
		base.RelativeScoreCutoff = 0.3
	case "outcomes":
		base.MinSemanticScore = 0.14
		base.MinTotalScore = 0.18
		base.RelativeScoreCutoff = 0.3
	}
	return base
}

func DefaultAdaptiveSignalTuning() AdaptiveSignalTuning {
	return AdaptiveSignalTuning{
		SalienceScoreFactor:       0.2,
		UsefulCountStep:           0.02,
		UsefulCountCap:            5,
		LastHelpfulRecencyWeight:  0.06,
		SuppressionScoreFactor:    0.22,
		RejectedCountStep:         0.03,
		RejectedCountCap:          5,
		HarmfulCountStep:          0.04,
		HarmfulCountCap:           5,
		LastRejectedRecencyWeight: 0.04,
		ActiveSuppressionBoost:    0.25,
		PinnedSuppressionFactor:   0.35,
		SuppressionBandThreshold:  0.35,
	}
}

func DefaultAdaptiveFeedbackTuning() AdaptiveFeedbackTuning {
	return AdaptiveFeedbackTuning{
		HelpfulSalienceDelta:         0.12,
		HelpfulSuppressionDelta:      -0.08,
		IgnoredSalienceDelta:         -0.02,
		IgnoredSuppressionDelta:      0.03,
		RejectedSalienceDelta:        -0.06,
		RejectedSuppressionDelta:     0.12,
		HarmfulSalienceDelta:         -0.1,
		HarmfulSuppressionDelta:      0.2,
		ConfirmedSalienceDelta:       0.08,
		ClarifiedSalienceDelta:       0.04,
		ContradictedSalienceDelta:    -0.08,
		ContradictedSuppressionDelta: 0.16,
		SupersededSalienceDelta:      -0.1,
		SupersededSuppressionDelta:   0.2,
		RejectedCooldown:             24 * time.Hour,
		HarmfulCooldown:              72 * time.Hour,
		ContradictedCooldown:         24 * time.Hour,
	}
}
