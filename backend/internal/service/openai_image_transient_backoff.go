package service

import "time"

const maxOpenAIImageTransientBackoff = 10 * time.Minute

// openAIImageTransientCooldownForAccount preserves the configured base
// cooldown for an isolated flap, then backs off repeated failures without
// changing the account's permanent status or its price priority.
func (s *OpenAIGatewayService) openAIImageTransientCooldownForAccount(account *Account, base time.Duration) time.Duration {
	if base <= 0 || s == nil || account == nil || s.openaiAccountStats == nil {
		return base
	}
	errorRate, _, _ := s.openaiAccountStats.snapshot(account.ID)
	multiplier := 1
	switch {
	case errorRate >= 0.75:
		multiplier = 8
	case errorRate >= 0.50:
		multiplier = 4
	case errorRate >= 0.25:
		multiplier = 2
	}

	cooldown := base * time.Duration(multiplier)
	if cooldown > maxOpenAIImageTransientBackoff {
		return maxOpenAIImageTransientBackoff
	}
	return cooldown
}
