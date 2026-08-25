// Package compiler implements PIACE's v3/v4 compiler catalog adapter and
// v3/v4 trusted-fact compatibility policy: task 6 ("Implement the v3/v4
// compiler adapter and trusted-fact policy"), design.md section 5
// ("Compiler request and compatibility policy"), and requirements.md
// 1.4-1.5, 2.3-2.6.
//
// *Adapter implements internal/capture.CompilerCatalogRequester (see
// internal/capture/compiler.go's doc comment for the exact boundary this
// package fills) so `capture catalog` and the future `compare` command
// share one compiler request/policy implementation rather than each
// having their own — design.md section 5's closing sentence is explicit
// about this: "Capture catalog uses the exact same adapter and policy as
// comparison."
//
// # Scope
//
// This package owns:
//
//   - v3 request encoding (POST /puppet/v3/catalog/:certname, form-
//     encoded body) and v4 request encoding (POST /puppet/v4/catalog,
//     JSON body), for both Puppet Server and OpenVox, per requirements.md
//     section 7's compatibility table;
//   - response shape validation and target identity/candidate-
//     environment verification, per design.md section 5's "Its contract
//     requires that the returned catalog identify the requested certname
//     and candidate environment exactly";
//   - v4 target trusted-fact handling: sending a factset's own valid
//     trusted-fact structure, using the documented v4 omitted-field/
//     compiler-lookup behavior only when the target has opted into that
//     compiler-side assumption, and failing the request outright when
//     neither is available, per design.md section 5's third paragraph;
//   - the v4-to-v3 fallback policy (opt-in only, and only for a verified
//     unsupported-v4 response) and the non-suppressible v3 trusted-fact
//     compatibility warning for every v3 catalog (including every
//     permitted fallback), per design.md section 5's fourth paragraph and
//     requirements.md 2.5-2.6.
//
// It does not normalize a catalog into model.NormalizedCatalog (task 7)
// and does not decide policy about OpenVox beyond what design.md section
// 5 states explicitly ("OpenVox is configured v3 only unless an operator
// explicitly selects an implementation with a documented v4 contract;
// PIACE does not claim v4 trusted-fact equivalence for OpenVox") — this
// package has no OpenVox-specific branch at all: it implements exactly
// the v3 and v4 request/response contracts documented for Puppet Server
// (which OpenVox's own documented v3 endpoint is wire-compatible with,
// per requirements.md section 7's "Shared catalog API" row), and an
// operator who selects catalog_api: v4 for an OpenVox compiler is relying
// on their own documented compatibility contract with that compiler, not
// on any OpenVox-specific behavior this package invents.
//
// # Wire shapes
//
// Per tasks.md's Notes ("Protocol adapters remain the compatibility
// boundary. Their exact requests and responses must be demonstrated with
// fixtures from the deployed service versions before declaring a
// compiler/PuppetDB combination supported"), this package is built from
// the v3/v4 catalog HTTP APIs as documented and as implemented in the
// compilers' own source:
//
//   - v3 request: POST /puppet/v3/catalog/<node>, form-encoded body with
//     `environment`, `facts_format=application/json`, a JSON-encoded
//     `facts` hash of the shape `{"name": <node>, "values": {...}}`, and
//     a generated `transaction_uuid`. Source: OpenVox's documented v3
//     catalog API (api/docs/http_catalog.md in openvoxproject/openvox),
//     which Puppet Server's v3 endpoint is wire-compatible with per
//     requirements.md section 7.
//   - catalog document: `{"name": <node>, "environment": ..., "code_id":
//     ..., "catalog_uuid": ..., "resources": [...], "edges": [...],
//     ...}`. Critically, this uses `name`, not `certname` — unlike
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
//   - v4 response envelope: `{"catalog": <document>}` — the v4 endpoint
//     wraps it, v3 does not. Source: puppetserver's own implementation,
//     src/ruby/puppetserver-lib/puppet/server/compiler.rb, whose
//     `compile` returns `{ catalog: catalog }` (or `{ catalog:, logs: }`
//     when options.capture_logs is set, which PIACE never sets), and
//     src/clj/puppetlabs/services/master/master_core.clj, whose
//     v4-catalog-fn JSON-encodes that hash verbatim as the 200 response
//     body. catalogDocument (response.go) unwraps it, keyed on the API
//     version of the request that produced the response — never sniffed
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
//     model.NormalizedCatalog is task 7's job, not this package's, and
//     task 7 must account for this documented shape difference between a
//     PuppetDB-sourced baseline and a compiler-sourced candidate.
//   - v4 request body: `{"certname", "persistence": {"facts": false,
//     "catalog": false}, "environment", "facts": {"values": {...}},
//     "trusted_facts": {"values": {...}}}`, matching Puppet Server's
//     CatalogRequestV4 schema in master_core.clj. `persistence` is always `{false,
//     false}` in every request this package builds — requirements.md
//     1.6 ("SHALL not persist candidate facts or candidate catalogs to
//     PuppetDB") makes this non-negotiable, not a configurable option.
//   - trusted facts: a factset's "trusted" fact (present in the classic
//     Puppet trusted-fact structure alongside ordinary facts, per
//     PuppetDB's own factsets documentation example) is the exact value
//     sent as the v4 request's `trusted_facts.values`. A "valid
//     trusted-fact structure" (design.md section 5) is judged as: the
//     "trusted" fact value decodes to a JSON object with a non-empty
//     string "certname" field and a present "authenticated" field — the
//     two fields that distinguish Puppet's documented trusted-fact shape
//     from an unrelated fact that happens to be named "trusted".
//
// # Verified-unsupported-v4 detection
//
// design.md section 3.1 permits a v4-to-v3 fallback "only for a
// documented unsupported-endpoint or unsupported-version response" and
// forbids it "after authentication, authorization, timeout, malformed
// response, or candidate identity/environment mismatch." Consistent with
// design's Error Handling section ("They do not preserve raw body text by
// default, because service errors can echo values"), this package makes
// that determination from HTTP status code alone, never from response
// body content: only a 404 (Not Found — the /puppet/v4/catalog route
// itself does not exist on this compiler, e.g. OpenVox or an older Puppet
// Server) or 501 (Not Implemented) response is treated as a verified
// unsupported-v4 signal. Every other status code (400, 401, 403, 5xx
// other than 501), a transport-layer failure (TLS/connect/timeout — see
// internal/transport's doc.go decision 4: those are always operational
// errors, and this package never reclassifies one as eligible for
// fallback), a malformed/unparseable response body, or an identity/
// environment mismatch is a plain compilation failure with no fallback,
// exactly as design.md section 3.1 requires.
//
// # No speculative version probing
//
// design.md section 5 states plainly: "PIACE does not probe alternate API
// versions speculatively." This package only ever attempts v3 alone, v4
// alone, or v4-then-v3 specifically because AllowV3Fallback is true and a
// verified-unsupported-v4 response was observed on that exact request —
// it never tries v4 "to see if it works" when v3 was configured, and
// never retries a second v4 request with different parameters.
package compiler
