package diff

import (
	"sort"

	"github.com/example42/piace/internal/model"
)

// diffEdges computes the edge-added/removed portion of pass 1 (see
// doc.go). Edges are compared as plain model.Edge values (Source/Target
// identity strings); internal/normalize's normalizer already sorts and deduplicates
// them by the ordered (Source, Target) pair, so a simple set-membership
// comparison over that pair is sufficient here.
func diffEdges(before, after []model.Edge) []model.EdgeChange {
	beforeSet := make(map[model.Edge]bool, len(before))
	for _, e := range before {
		beforeSet[e] = true
	}
	afterSet := make(map[model.Edge]bool, len(after))
	for _, e := range after {
		afterSet[e] = true
	}

	var changes []model.EdgeChange
	for _, e := range before {
		if !afterSet[e] {
			changes = append(changes, model.EdgeChange{Kind: model.ChangeEdgeRemoved, Edge: e})
		}
	}
	for _, e := range after {
		if !beforeSet[e] {
			changes = append(changes, model.EdgeChange{Kind: model.ChangeEdgeAdded, Edge: e})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Edge.Source != changes[j].Edge.Source {
			return changes[i].Edge.Source < changes[j].Edge.Source
		}
		if changes[i].Edge.Target != changes[j].Edge.Target {
			return changes[i].Edge.Target < changes[j].Edge.Target
		}
		return changes[i].Kind < changes[j].Kind
	})
	return changes
}
