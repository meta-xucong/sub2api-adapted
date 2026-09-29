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
}

type Policy struct {
	Enabled                 bool
	TopK                    int
	MaxAttemptsImage        int
	MaxAttemptsChat         int
	MaxAttemptsCompact      int
	MaxAttemptsDefault      int
	SameSourceGroupAttempts int
	CostBiasMax             float64
	Weights                 ScoreWeights
}

func DefaultPolicy() Policy {
	return Policy{
		TopK:                    5,
		MaxAttemptsImage:        2,
		MaxAttemptsChat:         3,
		MaxAttemptsCompact:      0,
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
		},
	}
}

func (p Policy) Normalize() Policy {
	d := DefaultPolicy()
	if p.TopK <= 0 {
		p.TopK = d.TopK
	}
	if p.MaxAttemptsImage <= 0 {
		p.MaxAttemptsImage = d.MaxAttemptsImage
	}
	if p.MaxAttemptsChat <= 0 {
		p.MaxAttemptsChat = d.MaxAttemptsChat
	}
	if p.MaxAttemptsDefault <= 0 {
		p.MaxAttemptsDefault = d.MaxAttemptsDefault
	}
	if p.SameSourceGroupAttempts <= 0 {
		p.SameSourceGroupAttempts = d.SameSourceGroupAttempts
	}
	if p.CostBiasMax <= 0 {
		p.CostBiasMax = d.CostBiasMax
	}
	if scoreWeightSum(p.Weights) <= 0 || scoreWeightsInvalid(p.Weights) {
		p.Weights = d.Weights
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
	if p.MaxAttemptsCompact < 0 {
		return fmt.Errorf("max_attempts_compact must be non-negative")
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
	case CapabilityResponsesCompact:
		return p.MaxAttemptsCompact
	default:
		return p.MaxAttemptsDefault
	}
}

func scoreWeightSum(w ScoreWeights) float64 {
	return w.Priority + w.Cost + w.Health + w.Load + w.Queue + w.Latency
}

func scoreWeightsInvalid(w ScoreWeights) bool {
	for _, value := range []float64{w.Priority, w.Cost, w.Health, w.Load, w.Queue, w.Latency} {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return true
		}
	}
	return false
}
