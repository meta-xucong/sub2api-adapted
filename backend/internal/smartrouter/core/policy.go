package core

import (
	"fmt"
	"math"
)

type ScoreWeights struct {
	Priority float64
	Cost     float64
	Health   float64
	Load     float64
	Queue    float64
	Latency  float64
	Recovery float64
}

type Policy struct {
	Enabled                 bool
	TopK                    int
	MaxAttemptsImage        int
	MaxAttemptsChat         int
	MaxAttemptsDefault      int
	SameSourceGroupAttempts int
	CostBiasMax             float64
	Weights                 ScoreWeights
}

func DefaultPolicy() Policy {
	return Policy{
		Enabled:                 false,
		TopK:                    5,
		MaxAttemptsImage:        2,
		MaxAttemptsChat:         3,
		MaxAttemptsDefault:      3,
		SameSourceGroupAttempts: 1,
		CostBiasMax:             3,
		Weights: ScoreWeights{
			Priority: 0.8,
			Cost:     1.0,
			Health:   1.2,
			Load:     1.0,
			Queue:    0.6,
			Latency:  0.4,
			Recovery: 0.8,
		},
	}
}

func (p Policy) Normalize() Policy {
	defaults := DefaultPolicy()
	if p.TopK <= 0 {
		p.TopK = defaults.TopK
	}
	if p.MaxAttemptsImage <= 0 {
		p.MaxAttemptsImage = defaults.MaxAttemptsImage
	}
	if p.MaxAttemptsChat <= 0 {
		p.MaxAttemptsChat = defaults.MaxAttemptsChat
	}
	if p.MaxAttemptsDefault <= 0 {
		p.MaxAttemptsDefault = defaults.MaxAttemptsDefault
	}
	if p.SameSourceGroupAttempts <= 0 {
		p.SameSourceGroupAttempts = defaults.SameSourceGroupAttempts
	}
	if p.CostBiasMax <= 0 {
		p.CostBiasMax = defaults.CostBiasMax
	}
	if scoreWeightSum(p.Weights) <= 0 || scoreWeightsInvalid(p.Weights) {
		p.Weights = defaults.Weights
	}
	return p
}

func (p Policy) Validate() error {
	if p.TopK < 0 {
		return fmt.Errorf("top_k must be non-negative")
	}
	if p.MaxAttemptsImage < 0 {
		return fmt.Errorf("max_attempts_image must be non-negative")
	}
	if p.MaxAttemptsChat < 0 {
		return fmt.Errorf("max_attempts_chat must be non-negative")
	}
	if p.MaxAttemptsDefault < 0 {
		return fmt.Errorf("max_attempts_default must be non-negative")
	}
	if p.SameSourceGroupAttempts < 0 {
		return fmt.Errorf("same_source_group_attempts must be non-negative")
	}
	if p.CostBiasMax < 0 {
		return fmt.Errorf("cost_bias_max must be non-negative")
	}
	if scoreWeightsInvalid(p.Weights) {
		return fmt.Errorf("score weights must be finite non-negative values")
	}
	return nil
}

func (p Policy) AttemptBudget(capability Capability) int {
	p = p.Normalize()
	switch capability {
	case CapabilityImageGeneration, CapabilityImageEdit:
		return p.MaxAttemptsImage
	case CapabilityChat, CapabilityResponses:
		return p.MaxAttemptsChat
	default:
		return p.MaxAttemptsDefault
	}
}

func scoreWeightSum(w ScoreWeights) float64 {
	return w.Priority + w.Cost + w.Health + w.Load + w.Queue + w.Latency + w.Recovery
}

func scoreWeightsInvalid(w ScoreWeights) bool {
	values := []float64{w.Priority, w.Cost, w.Health, w.Load, w.Queue, w.Latency, w.Recovery}
	for _, v := range values {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return true
		}
	}
	return false
}
