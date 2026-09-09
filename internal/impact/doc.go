// Package impact implements PIACE's impact estimator:
// `ImpactQuerier.Estimate(resourceIdentity, limits) -> ImpactEstimate`.
//
// # Scope, and why no estimate node is ever compiled
//
// This package issues read-only PQL queries against PuppetDB's query API
// and nothing else. There is no code path in it capable of reaching a
// compiler endpoint or a PuppetDB command or write endpoint: Querier
// holds one *transport.Client built from the resolved PuppetDB endpoint,
// and its only request builder targets a fixed query path. So the rule
// that PIACE never automatically compiles impact-estimate nodes is
// discharged structurally rather than by assertion, exactly as
// internal/puppetdb's doc.go discharges the no-mutation prohibition.
//
// Every estimate is a **Potential impact estimate**: it reports which
// nodes' latest stored catalog contains the exact resource, never that
// those nodes would change. This package emits no wording to the
// contrary and returns no field that could be read as a prediction.
//
// Rendering that label is deliberately not this package's job. Labelling
// the result is a property of the text, JSON and HTML output
// internal/report owns; model.ImpactEstimate carries state, not prose.
//
// # Endpoint: the entity-scoped path cannot take the mandated query
//
// The estimate queries PuppetDB for nodes whose latest stored catalog
// contains the changed exact resource type and title, using PQL. The
// exact query text is:
//
//	resources[certname] { type = <quoted-type> and title = <quoted-title> }
//
// Those cannot both be taken literally. PuppetDB's documented v4 query
// API (documentation/api/query/v4/overview.markdown and
// documentation/api/query/v4/resources.markdown in puppetlabs/puppetdb)
// splits the two forms:
//
//   - GET /pdb/query/v4, the root query endpoint, takes "either a PQL
//     query string or an AST JSON array specifying the query and
//     entity". A PQL query names its own entity, which is precisely what
//     the leading `resources[certname]` above does.
//   - GET /pdb/query/v4/resources, the entity-scoped endpoint, takes a
//     JSON-encoded (AST) query already scoped to resources. A PQL string
//     that re-names its entity is not a documented input there.
//
// The resolution used here: send that PQL text unmodified to the root
// endpoint. The exact generated PQL query has to be reported for an
// estimate, and the query text is specified verbatim, so the query text
// is the fixed obligation and the endpoint is what must accommodate it.
// Only the root endpoint can. The substance, querying for nodes whose
// latest stored catalog has the changed exact resource type and title,
// is satisfied exactly, since the PQL queries the resources entity; only
// the parenthetical `/pdb/query/v4/resources` path is superseded. The
// path actually used is recorded in every estimate
// (model.ImpactRequest.Path) so a report never leaves it implicit.
//
// # Measured against a deployed PuppetDB
//
// Every assumption in this section was checked against a deployed
// PuppetDB 8.15.0 on 2026-09-09, by issuing the requests this package
// builds and reading the responses:
//
//   - GET /pdb/query/v4 with the PQL text above in the `query`
//     parameter returns the matching certname rows. The root endpoint
//     does accept a PQL string that names its own entity.
//   - `limit` and `order_by` travel beside `query` as URL parameters and
//     are honoured. `order_by` is server-side, not decoration: the same
//     query with `asc` and with `desc` returned different three-row
//     samples of the same nine-row result, and the unlimited query
//     returned its rows in neither order, so a truncated sample without
//     `order_by` would not be reproducible.
//   - A query matching nothing returns `[]` with HTTP 200 and
//     content-type application/json, never `null` and never 404. See
//     parseCertnames for why `null` is refused all the same.
//   - A malformed PQL returns HTTP 400 with a text/plain body that
//     quotes the submitted query back. That body is never echoed into a
//     diagnostic, which is what keeps a resource title out of a report
//     by way of an error message.
//   - Every escape quotePQLString emits (backslash, double quote, \n,
//     \r, \t) is accepted by the parser: each returned HTTP 200 rather
//     than a parse error.
//
// What remains unverified is the behaviour of other supported PuppetDB
// versions, and whether a title containing one of those escapes matches
// a resource that really carries it, which needs a fixture catalog
// holding such a title.
//
// # Bounding, and the limit of what determinism can be promised
//
// The query is sent with `limit = result_limit + 1`, so receiving more
// than result_limit rows detects truncation without a second round trip
// or a true-total request. PuppetDB's documented paging parameters
// (documentation/api/query/v4/paging.markdown) are URL parameters
// supported on every query endpoint including the root one, so `limit`
// and `order_by` travel beside the `query` parameter and the PQL text
// stays byte-exact rather than growing an inline `order by ... limit N`
// clause.
//
// `order_by=[{"field":"certname","order":"asc"}]` is load-bearing, not
// decoration. Sorting certnames locally, which this package also always
// does, orders whatever subset came back, but when the true match count
// exceeds the limit, *which* subset PuppetDB returns is unconstrained
// without server-side ordering. Local sorting would then yield a
// deterministic ordering of a nondeterministic set, and a deterministic
// certname sample would not actually hold. With `order_by` honored, a
// truncated sample is the lexicographically first result_limit certnames
// and is reproducible. Against a PuppetDB that ignores or rejects
// `order_by`, a *truncated* sample is not reproducible; an untruncated
// one always is, because the full set is returned and sorted locally.
// That limit is stated here rather than left implied by the local sort.
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
// title containing any other control byte (U+0000 to U+001F) has no
// encoding this package can prove safe. Such an identity is skipped with
// a reported estimate_impact diagnostic naming the identity, never the
// offending bytes, rather than emitting a query that might be malformed
// or, worse, alter the query's meaning. This mirrors
// internal/filecontent's refusal to guess at an unsupported `source` URI
// scheme.
//
// # Failure classification
//
// Timeout, transport, PQL, or response errors become a separately
// reported failed estimate. Because an enabled estimate is requested
// analysis, an estimate failure contributes an operational outcome after
// all other targets finish. Every failure therefore produces both a
// model.ImpactEstimate with Status timeout or failed, which is what
// keeps query failures reported separately from catalog differences, and
// an error-severity model.OperationEstimateImpact diagnostic for the
// outcome reducer. A disabled estimate produces neither a request nor a
// failure.
package impact
