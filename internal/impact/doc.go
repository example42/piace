// Package impact implements PIACE's impact estimator: the second half of
// task 10 ("Build deterministic aggregate diffs and optional impact
// estimates"), design.md's Architecture component "impact estimator" and
// its named interface `ImpactQuerier.Estimate(resourceIdentity, limits)
// -> ImpactEstimate`, design.md section 8, and requirements.md 9.1-9.8.
//
// # Scope, and requirement 9.8 by construction
//
// This package issues read-only PQL queries against PuppetDB's query API
// and nothing else. There is no code path in it capable of reaching a
// compiler endpoint or a PuppetDB command/write endpoint: Querier holds
// one *transport.Client built from the resolved PuppetDB endpoint, and
// its only request builder targets a fixed query path. requirements.md
// 9.8 ("SHALL not automatically compile impact-estimate nodes in v1") is
// therefore discharged structurally rather than by assertion, exactly as
// internal/puppetdb's doc.go discharges the no-mutation prohibition.
//
// Every estimate is a **potential impact estimate**: it reports which
// nodes' latest stored catalog contains the exact resource, never that
// those nodes would change (requirements.md 9.3, design.md section 8).
// This package emits no wording to the contrary and returns no field
// that could be read as a prediction.
//
// Rendering that label is deliberately not this package's job.
// requirements.md 9.3 says the CLI "SHALL label the result", which is a
// property of the text/JSON/HTML output task 11 owns; model.ImpactEstimate
// carries state, not prose. Task 11's brief in tasks.md records the
// obligation explicitly so it does not fall between the two tasks.
//
// # Endpoint: reconciling requirements.md 9.2 with design.md section 8
//
// requirements.md 9.2 says to query `/pdb/query/v4/resources` using PQL.
// design.md section 8 fixes the exact query text:
//
//	resources[certname] { type = <quoted-type> and title = <quoted-title> }
//
// Those cannot both be taken literally. PuppetDB's documented v4 query
// API (documentation/api/query/v4/overview.markdown and
// documentation/api/query/v4/resources.markdown in puppetlabs/puppetdb)
// splits the two forms:
//
//   - GET /pdb/query/v4 — the root query endpoint — takes "either a PQL
//     query string or an AST JSON array specifying the query and
//     entity". A PQL query names its own entity, which is precisely what
//     the leading `resources[certname]` in design.md's text does.
//   - GET /pdb/query/v4/resources — the entity-scoped endpoint — takes a
//     JSON-encoded (AST) query already scoped to resources. A PQL string
//     that re-names its entity is not a documented input there.
//
// The resolution used here: send design.md section 8's PQL text
// unmodified to the root endpoint. requirements.md 9.4 mandates
// reporting "the exact generated PQL query used for an estimate" and
// design.md section 8 specifies that query verbatim, so the query text
// is the fixed obligation and the endpoint is what must accommodate it;
// only the root endpoint can. requirements.md 9.2's substance — query
// PuppetDB "for nodes whose latest stored catalog has the changed exact
// resource type and title" — is satisfied exactly, since the PQL queries
// the resources entity; only its parenthetical path is superseded. The
// path actually used is recorded in every estimate
// (model.ImpactRequest.Path) so a report never leaves it implicit.
//
// Per tasks.md's Notes ("Protocol adapters remain the compatibility
// boundary... their exact requests and responses must be demonstrated
// with fixtures from the deployed service versions"), this endpoint
// choice is a documented, fixture-unverified assumption for task 12 to
// confirm, in the same category as internal/puppetdb's and
// internal/compiler's own endpoint-shape assumptions.
//
// # Bounding, and the limit of what determinism can be promised
//
// Per design.md section 8 the query is sent with `limit = result_limit +
// 1`, so receiving more than result_limit rows detects truncation
// without a second round trip or a true-total request. PuppetDB's
// documented paging parameters (documentation/api/query/v4/paging.markdown)
// are URL parameters supported on every query endpoint including the
// root one, so `limit` and `order_by` travel beside the `query`
// parameter and design.md's PQL text stays byte-exact rather than
// growing an inline `order by ... limit N` clause.
//
// `order_by=[{"field":"certname","order":"asc"}]` is load-bearing, not
// decoration. Sorting certnames locally — which this package also always
// does, per design.md section 8 — orders whatever subset came back, but
// when the true match count exceeds the limit, *which* subset PuppetDB
// returns is unconstrained without server-side ordering. Local sorting
// would then yield a deterministic ordering of a nondeterministic set,
// and requirements.md 9.6's "deterministic certname sample" would not
// actually hold. With `order_by` honored, a truncated sample is the
// lexicographically first result_limit certnames and is reproducible.
// Against a PuppetDB that ignores or rejects `order_by`, a *truncated*
// sample is not reproducible; an untruncated one always is, because the
// full set is returned and sorted locally. That limit is stated here
// rather than left implied by the local sort.
//
// # PQL string literals, and titles that cannot be encoded
//
// PuppetDB's PQL reference (documentation/api/query/v4/pql.markdown)
// documents double-quoted strings as supporting escape sequences and
// single-quoted strings as not supporting escaping at all, so every
// literal this package emits is double-quoted. quotePQLString escapes
// backslash, double quote, and the three documented C-style escapes
// (\n, \r, \t).
//
// The documentation does not establish a \uXXXX form, so a resource
// title containing any other control byte (U+0000-U+001F) has no
// encoding this package can prove safe. Such an identity is skipped with
// a reported estimate_impact diagnostic naming the identity — never the
// offending bytes — rather than emitting a query that might be
// malformed or, worse, alter the query's meaning. This mirrors
// internal/filecontent's refusal to guess at an unsupported `source`
// URI scheme.
//
// # Failure classification
//
// design.md section 8: "Timeout, transport, PQL, or response errors
// become a separately reported failed estimate. Because an enabled
// estimate is requested analysis, an estimate failure contributes an
// operational outcome after all other targets finish." Every failure
// therefore produces both a model.ImpactEstimate with Status timeout or
// failed (so requirements.md 9.7's "report ... query failures separately
// from catalog differences" holds) and an error-severity
// model.OperationEstimateImpact diagnostic for task 11's reducer. A
// disabled estimate produces neither a request nor a failure.
package impact
