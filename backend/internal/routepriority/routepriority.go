// Package routepriority provides the pure ordering rules used by the optional
// Unified Gateway route-priority adapter. It intentionally knows nothing
// about accounts, configuration, persistence, billing, or request routing.
package routepriority

import (
	"errors"
	"sort"
)

// Candidate is one already-eligible scheduler candidate. Candidates can only
// be compared when ComparisonKey matches and both have equally-shaped prices.
type Candidate struct {
	ID            int64
	NativeIndex   int
	HealthLayer   int
	ComparisonKey string
	Prices        []float64
}

// Rank is an ephemeral request-local position. Lower layers are preferred.
type Rank struct {
	HealthLayer int
	PriceLayer  int
}

var ErrInvalidCandidates = errors.New("route priority candidates must have unique positive IDs")

// RankCandidates assigns stable Pareto layers independently inside each
// health layer. Strict Pareto dominance creates the only ordering edges.
func RankCandidates(candidates []Candidate) (map[int64]Rank, error) {
	ranks := make(map[int64]Rank, len(candidates))
	byHealth := make(map[int][]Candidate)
	for _, candidate := range candidates {
		if candidate.ID <= 0 {
			return nil, ErrInvalidCandidates
		}
		if _, exists := ranks[candidate.ID]; exists {
			return nil, ErrInvalidCandidates
		}
		ranks[candidate.ID] = Rank{HealthLayer: candidate.HealthLayer}
		byHealth[candidate.HealthLayer] = append(byHealth[candidate.HealthLayer], candidate)
	}

	for healthLayer, pool := range byHealth {
		layers, err := stableParetoLayers(pool)
		if err != nil {
			return nil, err
		}
		for layer, ids := range layers {
			for _, id := range ids {
				rank := ranks[id]
				rank.HealthLayer = healthLayer
				rank.PriceLayer = layer
				ranks[id] = rank
			}
		}
	}
	return ranks, nil
}

func stableParetoLayers(candidates []Candidate) ([][]int64, error) {
	count := len(candidates)
	if count == 0 {
		return nil, nil
	}
	index := make(map[int64]int, count)
	for i, candidate := range candidates {
		index[candidate.ID] = i
	}
	children := make([][]int, count)
	indegree := make([]int, count)
	for i := 0; i < count; i++ {
		for j := i + 1; j < count; j++ {
			if strictlyDominates(candidates[i], candidates[j]) {
				children[i] = append(children[i], j)
				indegree[j]++
			} else if strictlyDominates(candidates[j], candidates[i]) {
				children[j] = append(children[j], i)
				indegree[i]++
			}
		}
	}

	removed := make([]bool, count)
	layers := make([][]int64, 0, count)
	for removedCount := 0; removedCount < count; {
		ready := make([]int, 0)
		for i := range candidates {
			if !removed[i] && indegree[i] == 0 {
				ready = append(ready, i)
			}
		}
		if len(ready) == 0 {
			return nil, errors.New("route priority dominance graph contains a cycle")
		}
		sort.SliceStable(ready, func(i, j int) bool {
			left, right := candidates[ready[i]], candidates[ready[j]]
			if left.NativeIndex != right.NativeIndex {
				return left.NativeIndex < right.NativeIndex
			}
			return index[left.ID] < index[right.ID]
		})
		layer := make([]int64, 0, len(ready))
		for _, candidateIndex := range ready {
			removed[candidateIndex] = true
			removedCount++
			layer = append(layer, candidates[candidateIndex].ID)
			for _, child := range children[candidateIndex] {
				indegree[child]--
			}
		}
		layers = append(layers, layer)
	}
	return layers, nil
}

func strictlyDominates(left, right Candidate) bool {
	if left.ComparisonKey == "" || left.ComparisonKey != right.ComparisonKey || len(left.Prices) == 0 || len(left.Prices) != len(right.Prices) {
		return false
	}
	strict := false
	for i := range left.Prices {
		if left.Prices[i] > right.Prices[i] {
			return false
		}
		if left.Prices[i] < right.Prices[i] {
			strict = true
		}
	}
	return strict
}
