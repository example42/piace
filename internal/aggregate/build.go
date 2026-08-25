package aggregate

import (
	"sort"

	"github.com/example42/piace/internal/model"
)

// groupKey is the internal, comparable grouping key. It mirrors
// model.AggregateChangeKey but flattens the pointer fields (and adds the
// fingerprint) so it can be a Go map key. The public key is built from
// it at output time.
type groupKey struct {
	kind        model.ChangeKind
	resourceKey model.ResourceIdentity
	parameter   string
	edge        model.Edge
	fingerprint string
	// unfingerprintable distinguishes changes internal/diff could not
	// fingerprint. Each gets its own group (see doc.go), which a shared
	// empty fingerprint string alone would not achieve, so a per-change
	// sequence number is folded in via unfingerprintableSeq.
	unfingerprintableSeq int
}

// Build groups every change in nodeDiffs into a deterministic
// model.AggregateDiff. nodeDiffs is expected in target-file order; the
// output order does not depend on it (groups are fully sorted), but the
// per-group certname and reference ordering is stabilized independently
// of it too, so a caller reordering targets changes nothing.
func Build(nodeDiffs []model.NodeDiff) model.AggregateDiff {
	type member struct {
		certname string
		index    int
		before   any
		after    any
	}
	members := make(map[groupKey][]member)
	var order []groupKey

	unfingerprintable := 0
	add := func(key groupKey, m member) {
		if _, seen := members[key]; !seen {
			order = append(order, key)
		}
		members[key] = append(members[key], m)
	}

	for _, nd := range nodeDiffs {
		for i, change := range nd.ResourceChanges {
			key := groupKey{
				kind:        change.Kind,
				resourceKey: change.Identity,
				parameter:   change.Parameter,
				fingerprint: change.Fingerprint,
			}
			if change.Fingerprint == "" {
				unfingerprintable++
				key.unfingerprintableSeq = unfingerprintable
			}
			add(key, member{certname: nd.Certname, index: i, before: change.Before, after: change.After})
		}
		for i, change := range nd.EdgeChanges {
			key := groupKey{kind: change.Kind, edge: change.Edge}
			add(key, member{certname: nd.Certname, index: i})
		}
	}

	sortGroupKeys(order)

	groups := make([]model.AggregateGroup, 0, len(order))
	for _, key := range order {
		ms := members[key]
		sort.SliceStable(ms, func(i, j int) bool {
			if ms[i].certname != ms[j].certname {
				return ms[i].certname < ms[j].certname
			}
			return ms[i].index < ms[j].index
		})

		certnames := make([]string, 0, len(ms))
		refs := make([]model.NodeChangeRef, 0, len(ms))
		for _, m := range ms {
			if len(certnames) == 0 || certnames[len(certnames)-1] != m.certname {
				certnames = append(certnames, m.certname)
			}
			refs = append(refs, model.NodeChangeRef{Certname: m.certname, Index: m.index})
		}

		group := model.AggregateGroup{
			Key:            publicKey(key),
			Certnames:      certnames,
			NodeChangeRefs: refs,
		}
		// Every member of a resource group shares the same unredacted
		// evidence (that is what the fingerprint asserts), so the first
		// member's redacted projection represents the whole group.
		if !isEdgeKind(key.kind) {
			group.Before = ms[0].before
			group.After = ms[0].after
		}
		groups = append(groups, group)
	}

	return model.AggregateDiff{Groups: groups}
}

// publicKey converts an internal groupKey into the serializable
// model.AggregateChangeKey, setting exactly one of Identity and Edge
// according to kind.
func publicKey(key groupKey) model.AggregateChangeKey {
	out := model.AggregateChangeKey{Kind: key.kind}
	if isEdgeKind(key.kind) {
		edge := key.edge
		out.Edge = &edge
		return out
	}
	identity := key.resourceKey
	out.Identity = &identity
	out.Parameter = key.parameter
	return out
}

func isEdgeKind(kind model.ChangeKind) bool {
	return kind == model.ChangeEdgeAdded || kind == model.ChangeEdgeRemoved
}

// kindOrder fixes the emission order of the five change kinds, so
// "sorted by kind" (design.md section 9) is a defined total order rather
// than an accident of how the constants happen to spell out. Resource
// membership changes come first, then parameter changes, then edges,
// matching the order design.md section 7.1 lists them in.
var kindOrder = map[model.ChangeKind]int{
	model.ChangeResourceAdded:    0,
	model.ChangeResourceRemoved:  1,
	model.ChangeParameterChanged: 2,
	model.ChangeEdgeAdded:        3,
	model.ChangeEdgeRemoved:      4,
}

// sortGroupKeys orders groups by kind, then canonical identity or
// ordered edge pair, then parameter, then fingerprint. See doc.go for
// why the fingerprint tiebreaker is required rather than cosmetic.
func sortGroupKeys(keys []groupKey) {
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if ka, kb := kindOrder[a.kind], kindOrder[b.kind]; ka != kb {
			return ka < kb
		}
		if isEdgeKind(a.kind) {
			if a.edge.Source != b.edge.Source {
				return a.edge.Source < b.edge.Source
			}
			return a.edge.Target < b.edge.Target
		}
		if a.resourceKey.Type != b.resourceKey.Type {
			return a.resourceKey.Type < b.resourceKey.Type
		}
		if a.resourceKey.Title != b.resourceKey.Title {
			return a.resourceKey.Title < b.resourceKey.Title
		}
		if a.parameter != b.parameter {
			return a.parameter < b.parameter
		}
		if a.fingerprint != b.fingerprint {
			return a.fingerprint < b.fingerprint
		}
		return a.unfingerprintableSeq < b.unfingerprintableSeq
	})
}
