//go:build unit

package service

import (
	"context"
	"time"
)

// calculateOpenAIRecordUsageCost keeps unit tests focused on cost-only assertions.
func (s *OpenAIGatewayService) calculateOpenAIRecordUsageCost(
	ctx context.Context,
	result *OpenAIForwardResult,
	apiKey *APIKey,
	billingModels []string,
	multiplier float64,
	imageMultiplier float64,
	videoMultiplier float64,
	webSearchMultiplier float64,
	tokens UsageTokens,
	serviceTier string,
	longContextBillingGate *bool,
	pricingAt time.Time,
) (*CostBreakdown, error) {
	cost, _, err := s.calculateOpenAIRecordUsageCostWithModel(
		ctx, result, apiKey, billingModels, multiplier, imageMultiplier, videoMultiplier,
		webSearchMultiplier, tokens, serviceTier, longContextBillingGate, pricingAt,
	)
	return cost, err
}
