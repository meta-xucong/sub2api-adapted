package routepriority

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRankCandidatesBuildsStableParetoLayers(t *testing.T) {
	ranks, err := RankCandidates([]Candidate{
		{ID: 1, NativeIndex: 0, ComparisonKey: "m", Prices: []float64{1, 4}},
		{ID: 2, NativeIndex: 1, ComparisonKey: "m", Prices: []float64{2, 2}},
		{ID: 3, NativeIndex: 2, ComparisonKey: "m", Prices: []float64{3, 5}},
		{ID: 4, NativeIndex: 3, ComparisonKey: "m", Prices: []float64{3, 5}},
	})
	require.NoError(t, err)
	require.Equal(t, Rank{HealthLayer: 0, PriceLayer: 0}, ranks[1])
	require.Equal(t, Rank{HealthLayer: 0, PriceLayer: 0}, ranks[2])
	require.Equal(t, Rank{HealthLayer: 0, PriceLayer: 1}, ranks[3])
	require.Equal(t, Rank{HealthLayer: 0, PriceLayer: 1}, ranks[4])
}

func TestRankCandidatesHonorsTransitiveDominanceAndHealthBoundary(t *testing.T) {
	ranks, err := RankCandidates([]Candidate{
		{ID: 1, NativeIndex: 0, HealthLayer: 0, ComparisonKey: "m", Prices: []float64{1, 5}},
		// 1 and 2 are incomparable; candidate 3 independently dominates 2,
		// so stable Kahn layering places 2 after the layer containing 1 and 3.
		{ID: 2, NativeIndex: 1, HealthLayer: 0, ComparisonKey: "m", Prices: []float64{4, 3}},
		{ID: 3, NativeIndex: 2, HealthLayer: 0, ComparisonKey: "m", Prices: []float64{2, 3}},
		{ID: 4, NativeIndex: 3, HealthLayer: 1, ComparisonKey: "m", Prices: []float64{0, 0}},
	})
	require.NoError(t, err)
	require.Equal(t, Rank{HealthLayer: 0, PriceLayer: 0}, ranks[1])
	require.Equal(t, Rank{HealthLayer: 0, PriceLayer: 0}, ranks[3])
	require.Equal(t, Rank{HealthLayer: 0, PriceLayer: 1}, ranks[2])
	require.Equal(t, Rank{HealthLayer: 1, PriceLayer: 0}, ranks[4])
}

func TestRankCandidatesLeavesIncompatibleCardsWithoutEdges(t *testing.T) {
	candidates := []Candidate{
		{ID: 1, NativeIndex: 0, ComparisonKey: "multiplier:gpt", Prices: []float64{0.4}},
		{ID: 2, NativeIndex: 1, ComparisonKey: "base:gpt", Prices: []float64{1, 1, 1}},
		{ID: 3, NativeIndex: 2, ComparisonKey: "multiplier:claude", Prices: []float64{0.1}},
		{ID: 4, NativeIndex: 3, ComparisonKey: "base:gpt:optional-cache-write", Prices: []float64{1, 1, 1, 1}},
		{ID: 5, NativeIndex: 4, ComparisonKey: "base:gpt", Prices: []float64{0.5, 0.5}},
	}
	ranks, err := RankCandidates(candidates)
	require.NoError(t, err)
	for _, candidate := range candidates {
		require.Equal(t, 0, ranks[candidate.ID].PriceLayer)
	}
}

func TestRankCandidatesUsesNativeOrderForTiesAndNoPriceEvidence(t *testing.T) {
	ranks, err := RankCandidates([]Candidate{
		{ID: 8, NativeIndex: 1, ComparisonKey: "m", Prices: []float64{1}},
		{ID: 7, NativeIndex: 0, ComparisonKey: "m", Prices: []float64{1}},
		{ID: 9, NativeIndex: 2},
	})
	require.NoError(t, err)
	require.Equal(t, ranks[7].PriceLayer, ranks[8].PriceLayer)
	require.Equal(t, 0, ranks[9].PriceLayer)
}

func TestRankCandidatesRejectsDuplicateOrInvalidIDs(t *testing.T) {
	_, err := RankCandidates([]Candidate{{ID: 1}, {ID: 1}})
	require.ErrorIs(t, err, ErrInvalidCandidates)
	_, err = RankCandidates([]Candidate{{ID: 0}})
	require.ErrorIs(t, err, ErrInvalidCandidates)
}
