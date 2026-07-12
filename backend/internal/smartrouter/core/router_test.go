package core

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOrder_FiltersCapabilityModelAndConcurrencyButRetainsCooldown(t *testing.T) {
	policy := DefaultPolicy()
	policy.Enabled = true
	plan := Order(RouteRequest{
		Model:      "gpt-image-2",
		Capability: CapabilityImageEdit,
		NowUnix:    100,
		Seed:       42,
	}, []LaneSnapshot{
		{LaneID: "ok", AccountID: 1, SourceGroup: "ok", Capabilities: map[Capability]bool{CapabilityImageEdit: true}, ModelPatterns: []string{"gpt-image-*"}, MaxConcurrency: 2, CurrentConcurrency: 1},
		{LaneID: "chat", AccountID: 2, Capabilities: map[Capability]bool{CapabilityChat: true}, ModelPatterns: []string{"gpt-image-*"}},
		{LaneID: "model", AccountID: 3, Capabilities: map[Capability]bool{CapabilityImageEdit: true}, ModelPatterns: []string{"gpt-5.*"}},
		{LaneID: "cool", AccountID: 4, SourceGroup: "cool", Capabilities: map[Capability]bool{CapabilityImageEdit: true}, ModelPatterns: []string{"gpt-image-*"}, CooldownUntilUnix: 200},
		{LaneID: "full", AccountID: 5, Capabilities: map[Capability]bool{CapabilityImageEdit: true}, ModelPatterns: []string{"gpt-image-*"}, MaxConcurrency: 1, CurrentConcurrency: 1},
	}, policy)

	require.NotEmpty(t, plan.OrderedLaneIDs)
	require.Contains(t, plan.OrderedLaneIDs, "ok")
	require.Contains(t, plan.OrderedLaneIDs, "cool")
	require.Equal(t, "capability_mismatch", plan.SkipReasons["chat"])
	require.Equal(t, "model_mismatch", plan.SkipReasons["model"])
	require.NotContains(t, plan.SkipReasons, "cool")
	require.Equal(t, "lane_concurrency_full", plan.SkipReasons["full"])
}

func TestOrder_AllLanesCoolingStillLeavesLastResortCandidates(t *testing.T) {
	policy := DefaultPolicy()
	policy.Enabled = true
	plan := Order(RouteRequest{Capability: CapabilityImageGeneration, NowUnix: 100, Seed: 43}, []LaneSnapshot{
		{LaneID: "cool-a", AccountID: 1, Priority: 30, SourceGroup: "a", CooldownUntilUnix: 200},
		{LaneID: "cool-b", AccountID: 2, Priority: 31, SourceGroup: "b", CooldownUntilUnix: 200},
		{LaneID: "cool-c", AccountID: 3, Priority: 32, SourceGroup: "c", CooldownUntilUnix: 200},
	}, policy)

	require.NotEmpty(t, plan.OrderedLaneIDs)
	require.Contains(t, plan.OrderedLaneIDs, "cool-a")
	// Priority layering still chooses the least-bad lane first; a failed
	// attempt excludes it and the next scheduling pass advances to the next.
	require.Len(t, plan.Candidates, 1)
}

func TestOrder_RespectsAttemptBudget(t *testing.T) {
	policy := DefaultPolicy()
	policy.Enabled = true
	policy.MaxAttemptsImage = 2
	lanes := []LaneSnapshot{
		{LaneID: "one", AccountID: 1},
		{LaneID: "two", AccountID: 2},
		{LaneID: "three", AccountID: 3},
	}

	first := Order(RouteRequest{Capability: CapabilityImageGeneration, AttemptNumber: 0, Seed: 7}, lanes, policy)
	require.Len(t, first.OrderedLaneIDs, 2)

	exhausted := Order(RouteRequest{Capability: CapabilityImageGeneration, AttemptNumber: 2, Seed: 7}, lanes, policy)
	require.Empty(t, exhausted.OrderedLaneIDs)
}

func TestOrder_BlocksAttemptWhenRemainingBudgetCannotFit(t *testing.T) {
	policy := DefaultPolicy()
	policy.Enabled = true

	plan := Order(RouteRequest{
		Capability:                 CapabilityImageGeneration,
		RemainingBudgetSeconds:     195,
		MinimumAttemptSeconds:      180,
		FinalizationReserveSeconds: 15,
	}, []LaneSnapshot{{LaneID: "fallback", AccountID: 1}}, policy)

	require.True(t, plan.BudgetBlocked)
	require.Empty(t, plan.OrderedLaneIDs)
	require.Equal(t, "insufficient_remaining_budget", plan.SkipReasons["__budget__"])
}

func TestOrder_StaticCapabilityMapKeepsImageLanesSeparate(t *testing.T) {
	policy := DefaultPolicy()
	policy.Enabled = true
	plan := Order(RouteRequest{Capability: CapabilityImageGeneration}, []LaneSnapshot{
		{LaneID: "flowyun-edit-only", AccountID: 1, Capabilities: map[Capability]bool{CapabilityImageEdit: true}},
		{LaneID: "stable-generation", AccountID: 2, Capabilities: map[Capability]bool{CapabilityImageGeneration: true}},
	}, policy)

	require.Equal(t, []string{"stable-generation"}, plan.OrderedLaneIDs)
	require.Equal(t, "capability_mismatch", plan.SkipReasons["flowyun-edit-only"])
}

func TestOrder_PrefersMatchingImageSizeSpecialistThenFallsBackToGenericLane(t *testing.T) {
	policy := DefaultPolicy()
	policy.Enabled = true
	lanes := []LaneSnapshot{
		{LaneID: "generic-cheap", AccountID: 1, Priority: 1, Capabilities: map[Capability]bool{CapabilityImageGeneration: true}},
		{LaneID: "super-resolution", AccountID: 2, Priority: 9, Capabilities: map[Capability]bool{CapabilityImageGeneration: true}, ImageSizeTiers: []string{"2K", "4K"}},
		{LaneID: "one-k", AccountID: 3, Priority: 2, Capabilities: map[Capability]bool{CapabilityImageGeneration: true}, ImageSizeTiers: []string{"1K"}},
	}

	twoK := Order(RouteRequest{Capability: CapabilityImageGeneration, ImageSizeTier: "2k", Seed: 31}, lanes, policy)
	require.Equal(t, []string{"super-resolution"}, twoK.OrderedLaneIDs)
	require.Equal(t, "image_size_mismatch", twoK.SkipReasons["one-k"])

	fallback := Order(RouteRequest{
		Capability:      CapabilityImageGeneration,
		ImageSizeTier:   "2K",
		Seed:            32,
		ExcludedLaneIDs: map[string]struct{}{"super-resolution": {}},
	}, lanes, policy)
	require.Equal(t, []string{"generic-cheap"}, fallback.OrderedLaneIDs)
	require.Equal(t, "excluded_lane", fallback.SkipReasons["super-resolution"])

	oneK := Order(RouteRequest{Capability: CapabilityImageGeneration, ImageSizeTier: "1K", Seed: 33}, lanes, policy)
	require.Equal(t, []string{"one-k"}, oneK.OrderedLaneIDs)
}

func TestOrder_SourceGroupGuard(t *testing.T) {
	policy := DefaultPolicy()
	policy.Enabled = true
	policy.SameSourceGroupAttempts = 1

	plan := Order(RouteRequest{
		Capability: CapabilityChat,
		ExcludedSourceGroups: map[string]struct{}{
			"same": {},
		},
		Seed: 9,
	}, []LaneSnapshot{
		{LaneID: "same-a", AccountID: 1, SourceGroup: "same"},
		{LaneID: "same-b", AccountID: 2, SourceGroup: "same"},
		{LaneID: "other", AccountID: 3, SourceGroup: "other"},
	}, policy)

	require.Equal(t, []string{"other"}, plan.OrderedLaneIDs)
	require.Equal(t, "excluded_source_group", plan.SkipReasons["same-a"])
	require.Equal(t, "excluded_source_group", plan.SkipReasons["same-b"])
}

func TestOrder_SourceGroupMaxConcurrency(t *testing.T) {
	policy := DefaultPolicy()
	policy.Enabled = true
	plan := Order(RouteRequest{Capability: CapabilityChat, Seed: 11}, []LaneSnapshot{
		{LaneID: "busy", AccountID: 1, SourceGroup: "group", CurrentConcurrency: 1, SourceGroupMaxConcurrency: 1},
		{LaneID: "also-busy", AccountID: 2, SourceGroup: "group", CurrentConcurrency: 0, SourceGroupMaxConcurrency: 1},
		{LaneID: "open", AccountID: 3, SourceGroup: "open", SourceGroupMaxConcurrency: 1},
	}, policy)

	require.Equal(t, []string{"open"}, plan.OrderedLaneIDs)
	require.Equal(t, "source_group_concurrency_full", plan.SkipReasons["busy"])
	require.Equal(t, "source_group_concurrency_full", plan.SkipReasons["also-busy"])
}

func TestOrder_CostBiasPrefersCheapButKeepsFallbackCandidate(t *testing.T) {
	policy := DefaultPolicy()
	policy.Enabled = true
	policy.CostBiasMax = 2
	plan := Order(RouteRequest{Capability: CapabilityChat, Seed: 13}, []LaneSnapshot{
		{LaneID: "cheap", AccountID: 1, CostMultiplier: 0.1, BaseWeight: 1, Priority: 1},
		{LaneID: "fallback", AccountID: 2, CostMultiplier: 1, BaseWeight: 1, Priority: 1},
	}, policy)

	require.Len(t, plan.Candidates, 2)
	require.Greater(t, plan.Candidates[0].Score, plan.Candidates[1].Score)
	require.ElementsMatch(t, []string{"cheap", "fallback"}, plan.OrderedLaneIDs)
}

func TestOrder_RestrictsRoutingToCurrentPriorityLayer(t *testing.T) {
	policy := DefaultPolicy()
	policy.Enabled = true
	policy.CostBiasMax = 10
	plan := Order(RouteRequest{Capability: CapabilityImageEdit, Seed: 17}, []LaneSnapshot{
		{LaneID: "k12-a", AccountID: 1, CostMultiplier: 3, BaseWeight: 1, Priority: 1},
		{LaneID: "k12-b", AccountID: 2, CostMultiplier: 2, BaseWeight: 1, Priority: 1},
		{LaneID: "cheap-fallback", AccountID: 3, CostMultiplier: 0.1, BaseWeight: 1, Priority: 2},
		{LaneID: "last-resort", AccountID: 4, CostMultiplier: 0.1, BaseWeight: 1, Priority: 3},
	}, policy)

	require.Len(t, plan.Candidates, 2)
	require.ElementsMatch(t, []string{"k12-a", "k12-b"}, plan.OrderedLaneIDs)
	for _, candidate := range plan.Candidates {
		require.NotEqual(t, "cheap-fallback", candidate.LaneID)
		require.NotEqual(t, "last-resort", candidate.LaneID)
	}
}

func TestOrder_AdvancesPriorityLayerAfterCurrentLayerExcluded(t *testing.T) {
	policy := DefaultPolicy()
	policy.Enabled = true
	plan := Order(RouteRequest{
		Capability: CapabilityImageEdit,
		Seed:       19,
		ExcludedLaneIDs: map[string]struct{}{
			"k12-a": {},
			"k12-b": {},
		},
	}, []LaneSnapshot{
		{LaneID: "k12-a", AccountID: 1, Priority: 1},
		{LaneID: "k12-b", AccountID: 2, Priority: 1},
		{LaneID: "fallback", AccountID: 3, Priority: 2},
		{LaneID: "last-resort", AccountID: 4, Priority: 3},
	}, policy)

	require.Equal(t, []string{"fallback"}, plan.OrderedLaneIDs)
	require.Len(t, plan.Candidates, 1)
	require.Equal(t, "fallback", plan.Candidates[0].LaneID)
}

func TestClassifyFailure(t *testing.T) {
	require.Equal(t, FailureTransientForbidden, ClassifyFailure(http.StatusForbidden, CapabilityImageEdit, false))
	require.Equal(t, FailureAuthForbidden, ClassifyFailure(http.StatusForbidden, CapabilityChat, false))
	require.Equal(t, FailureRateLimited, ClassifyFailure(http.StatusTooManyRequests, CapabilityChat, false))
	require.Equal(t, FailureTimeout, ClassifyFailure(http.StatusGatewayTimeout, CapabilityChat, false))
	require.Equal(t, FailureCancelled, ClassifyFailure(http.StatusBadGateway, CapabilityChat, true))
}
