// Package compiler implements PIACE's v3/v4 compiler catalog adapter and
// its v3/v4 trusted-fact compatibility policy.
//
// *Adapter implements internal/capture.CompilerCatalogRequester (see
// internal/capture/compiler.go's doc comment for the exact boundary this
// package fills) so `capture catalog` and `compare` share one compiler
// request and policy implementation rather than each having their own.
// Capture catalog uses the exact same adapter and policy as comparison.
//
// # Scope
//
// This package owns:
//
//   - v3 request encoding (POST /puppet/v3/catalog/:certname, form-
//     encoded body) and v4 request encoding (POST /puppet/v4/catalog,
//     JSON body), for both Puppet Server and OpenVox;
//   - response shape validation and target identity and candidate
//     environment verification: the returned catalog has to identify the
//     requested certname and candidate environment exactly;
//   - v4 persistence suppression: every v4 request carries
//     `persistence: {facts: false, catalog: false}`, unconditionally.
//     This is the only client-side control that keeps a candidate
//     compilation out of PuppetDB, and it is why the no-persistence rule
//     holds for v4 and cannot hold for v3 (see "# Persistence" below);
//   - v4 target trusted-fact handling: sending a factset's own valid
//     trusted-fact structure, using the documented v4 omitted-field/
//     compiler-lookup behavior only when the target has opted into that
//     compiler-side assumption, and failing the request outright when
//     neither is available;
//   - the v4-to-v3 fallback policy (opt-in only, and only for a verified
//     unsupported-v4 response) and the non-suppressible v3 trusted-fact
//     compatibility warning for every v3 catalog, permitted fallbacks
//     included.
//
// It does not normalize a catalog into model.NormalizedCatalog (internal/normalize).
//
// This package has no implementation-specific branch: Puppet Server and
// OpenVox serve the same v3 and v4 catalog contracts, both authorize a
// catalog-reader certificate through auth.conf, and both honour the v4
// request's persistence field, verified against a deployed OpenVox
// compiler on 2026-08-25. The configured catalog_api, not the compiler
// product, decides what this package can guarantee.
//
// # Wire shapes
//
// Protocol adapters are the compatibility boundary, and their exact
// requests and responses have to be demonstrated with fixtures from the
// deployed service versions before a compiler and PuppetDB combination
// is declared supported. This package is built from the v3 and v4
// catalog HTTP APIs as documented and as implemented in the compilers'
// own source:
//
//   - v3 request: POST /puppet/v3/catalog/<node>, form-encoded body with
//     `environment`, `facts_format=application/json`, a JSON-encoded
//     `facts` hash of the shape `{"name": <node>, "values": {...}}`, and
//     a generated `transaction_uuid`. Source: OpenVox's documented v3
//     catalog API (api/docs/http_catalog.md in openvoxproject/openvox),
//     which Puppet Server's v3 endpoint is wire-compatible with.
//   - v3 request Accept header: `Accept: application/json`, and it is
//     mandatory, not a nicety. Unlike v4 (a pure Clojure route in
//     master_core.clj), the v3 catalog endpoint dispatches into the
//     compiler's embedded Ruby Puppet request handler, whose
//     Puppet::Network::HTTP::Request#response_formatters_for raises
//     "Missing required Accept header" when the header is absent, so the
//     request is rejected before any compilation happens. Verified
//     against a deployed OpenVox server (2026-08-25): the same POST,
//     with real PuppetDB-sourced facts, returns
//     `{"message":"Bad Request: Missing required Accept header",
//     "issue_kind":"MISSING_HEADER_FIELD"}` with HTTP 400 when the
//     header is omitted and HTTP 200 with a complete catalog when it is
//     `application/json`. The same request against /puppet/v4/catalog
//     succeeds with no Accept header at all, confirming the asymmetry.
//     buildV3Request therefore sets the header and buildV4Request
//     deliberately does not.
//   - v3 rich-data encoding is not selected by the Accept header, at
//     least on the compiler this was measured against. A Puppet agent
//     requests `application/vnd.puppet.rich+json, application/json,
//     text/pson`, which raises a fair question for PIACE: a
//     PuppetDB-sourced baseline was stored from a real agent's
//     submission, so a candidate fetched with a *less* capable Accept
//     could differ from it in encoding alone, with rich types (Sensitive,
//     Timestamp, Binary, Regexp, Deferred) degrading to plain strings
//     and produce diffs that are pure artifacts. Measured on the same
//     deployed OpenVox server, it does not: the two responses differ
//     only in `Content-Type` (application/json vs
//     application/vnd.puppet.rich+json) and in the per-compilation
//     `catalog_uuid`/`version`; the catalog documents are structurally
//     identical, and `__ptype`-tagged rich values (a Regexp parameter)
//     appear in *both*. The isolating case was run too:
//     `Accept: application/vnd.puppet.rich+json` alone, with no
//     application/json fallback for the server to select instead,
//     and returns the same structurally identical document, so the
//     result is not an artifact of the agent list's json fallback
//     matching first. Rich encoding is a server-side property
//     (Puppet's `rich_data` setting), not something the client's Accept
//     header negotiates. Caveat on the sample: the catalog used carried
//     rich values of one type only (Regexp), so this is evidence that
//     the converter's rich flag is on regardless of requested format,
//     not a per-type enumeration. Requesting bare `application/json`
//     keeps this package's Accept header minimal and honest about what
//     response.go actually decodes; if a future deployment is found
//     where the header does select the encoding, this constant, not
//     normalization, is the place to change it.
//   - catalog document: `{"name": <node>, "environment": ..., "code_id":
//     ..., "catalog_uuid": ..., "resources": [...], "edges": [...],
//     ...}`. Critically, this uses `name`, not `certname`, unlike
//     internal/puppetdb's query-API responses. This package's
//     wireCatalog type reflects that; RequestCandidate maps
//     wireCatalog.Name into the returned puppetdb.Catalog's Certname
//     field so the rest of the codebase (which already keys everything
//     on Certname) does not need a second identity field name. Version
//     is accepted as either a JSON string or number (the OpenVox example
//     response shows a bare integer; PuppetDB's own query-API catalog
//     responses show a string) via wireCatalog's custom decoding.
//   - v3 response envelope: none. `POST /puppet/v3/catalog/<node>`
//     returns the catalog document as the entire response body. Source:
//     the example response in OpenVox's api/docs/http_catalog.md (and
//     puppetlabs/puppet's identical copy of that file).
//   - v4 response envelope: `{"catalog": <document>}`. The v4 endpoint
//     wraps it, v3 does not. Source: puppetserver's own implementation,
//     src/ruby/puppetserver-lib/puppet/server/compiler.rb, whose
//     `compile` returns `{ catalog: catalog }` (or `{ catalog:, logs: }`
//     when options.capture_logs is set, which PIACE never sets), and
//     src/clj/puppetlabs/services/master/master_core.clj, whose
//     v4-catalog-fn JSON-encodes that hash verbatim as the 200 response
//     body. catalogDocument (response.go) unwraps it, keyed on the API
//     version of the request that produced the response, never sniffed
//     from the body. This difference is silent if unhandled: a v4 body
//     decodes cleanly into wireCatalog with every field absent, so an
//     unwrapped read reports "malformed response" for a compilation the
//     compiler's own log records as successful.
//   - `resources`/`edges` are plain JSON arrays in the compiler's
//     catalog document, not the `{href, data}` expansion internal/puppetdb's
//     doc.go documents for a PuppetDB *query-API* catalog response. This
//     package passes them through as-is (puppetdb.Catalog.Resources/
//     Edges are already typed json.RawMessage precisely so a later stage
//     can parse either shape); normalizing either shape into
//     model.NormalizedCatalog is internal/normalize's job, not this package's, and
//     internal/normalize must account for this documented shape difference between a
//     PuppetDB-sourced baseline and a compiler-sourced candidate.
//   - v4 request body: `{"certname", "persistence": {"facts": false,
//     "catalog": false}, "environment", "facts": {"values": {...}},
//     "trusted_facts": {"values": {...}}}`, matching Puppet Server's
//     CatalogRequestV4 schema in master_core.clj. `persistence` is always
//     `{false, false}` in every request this package builds: PIACE never
//     persists candidate facts or candidate catalogs to PuppetDB, which
//     makes this non-negotiable rather than a configurable option.
//   - trusted facts: internal/puppetdb.ValidateFactset checks input identity,
//     collection structure and supplied trusted facts before any v3/v4 request.
//     Supplied certname must equal the target, authenticated must be "remote"
//     or "local", optional extensions/external must be objects, and optional
//     hostname/domain must agree with certname (null domain is valid without a
//     domain suffix). Puppet's unauthenticated context uses boolean false and a
//     null certname; it cannot establish the target identity PIACE requires.
//     Source: lib/puppet/context/trusted_information.rb in puppetlabs/puppet.
//     Missing trusted facts may use an explicitly configured compiler lookup.
//     Malformed or inconsistent supplied trusted facts fail operationally even
//     when lookup is enabled. Missing facts without lookup remain a v4 policy
//     failure. These checks validate structure and attribution, not authenticity
//     of operator-supplied snapshot contents.
//
// # Persistence
//
// A v4 request suppresses persistence and a v3 request cannot, and the
// difference is contractual rather than cosmetic. Verified against a
// deployed OpenVox compiler on 2026-08-25 with a certname PuppetDB had
// never seen:
//
//   - `POST /puppet/v4/catalog` with `persistence: {facts: false,
//     catalog: false}` returned the catalog and left PuppetDB with no
//     factset, no catalog, and no node for that certname;
//   - `POST /puppet/v3/catalog/<certname>` returned the catalog and left
//     PuppetDB holding a factset and a catalog for it under the requested
//     environment, the stored catalog carrying the request's own
//     transaction_uuid, and a node whose facts_environment and
//     catalog_environment were both the candidate environment.
//
// The v3 endpoint has no persistence parameter to set: the compiler
// saves the facts submitted with the request, and stores the compiled
// catalog through its PuppetDB catalog cache terminus. For a real target
// this overwrites the target's stored factset and catalog, which is
// exactly the PuppetDB baseline a comparison reads, so a v3 candidate
// compilation destroys its own run's baseline for every subsequent
// target. That is why a v3 target is constrained to baseline.source:
// file, and why the v3 warning covers persistence as well as $trusted.
// This package cannot prevent either effect; it sends the v4 persistence
// fields where they exist and reports the v3 consequences where they do
// not.
//
// # Explicit v4 fallback
//
// Only an empty or plain "Not Found" HTTP 404 is eligible, and only with
// AllowV3Fallback. This is an explicitly accepted ambiguity, not proof that v4
// is unsupported: a proxy can generate the same response. A warning records
// that limitation on fallback. 501 has no verified Puppet Server contract and
// no longer triggers fallback. Structured errors, malformed bodies, HTML,
// authentication failures and timeouts never trigger fallback.
//
// Source inspection of Puppet Server master_core.clj shows v3/v4 route
// registration and other uses of 404, including missing environments. Phase 2
// tests are synthetic and do not establish every proxy or legacy server's
// absent-route body. An unrecognized failure requires selecting v3 explicitly.
//
// Every attempted v3 request returns effective API and persistence warnings,
// even if transport or response validation fails. Comparison rejects any v3
// policy with a PuppetDB baseline before network I/O; capture does not use that
// comparison-only guard. Static metadata and recursive_metadata survive
// compiler decoding for content evidence and snapshot capture.
package compiler
