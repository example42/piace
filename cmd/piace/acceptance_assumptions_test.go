package main

import "testing"

// This file records the acceptance conditions in tasks.md task 12 that
// CANNOT be discharged by the fixture-driven suite, so that a fully green
// test run is never mistaken for full acceptance.
//
// Each condition below asks for confirmation against a *deployed*
// service. A fixture cannot supply it: the suite's fake PuppetDB and fake
// compiler are built from the same documented assumptions the code under
// test was built from, so a passing test proves the two agree with each
// other, not that either agrees with a real Puppet installation. Serving
// an assumption back to the code that assumes it is circular.
//
// These are deliberately expressed as skipped tests rather than as a
// comment in a document. A skip is visible in every `go test -v` run and
// in CI output, and it sits next to the tests that would otherwise be
// read as covering the same ground.

// TestOutstanding_PuppetDBImpactEndpointAssumptions is tasks.md task 12's
// second bullet.
//
// What must be confirmed against a deployed PuppetDB:
//
//  1. design.md section 8's PQL text —
//     `resources[certname] { type = <quoted-type> and title = <quoted-title> }`
//     — is accepted at the ROOT endpoint `/pdb/query/v4`.
//     requirements.md 9.2 names `/pdb/query/v4/resources`, which takes an
//     AST query already scoped to resources, not a PQL string that names
//     its own entity. internal/impact/doc.go explains why the root
//     endpoint is the only one that can accept the mandated text; that
//     reasoning is from PuppetDB's documentation, not from a live
//     response.
//
//  2. The `limit` and `order_by` URL parameters are honored alongside a
//     `query` parameter at that endpoint. This one has a consequence, not
//     just a risk: without an honored `order_by`, *which* subset PuppetDB
//     returns for an over-limit query is unconstrained, so a TRUNCATED
//     impact sample is not reproducible — and requirements.md 9.6 assumes
//     it is. An untruncated sample stays reproducible either way, because
//     the full set is returned and sorted locally.
//
// What IS already covered, and why it is not enough:
// TestAcceptance_ImpactQueryWireShape asserts the exact path, query text,
// `limit` (result_limit + 1), and `order_by` PIACE puts on the wire. That
// pins PIACE's side of the contract and will fail loudly if it changes.
// It says nothing about what a real PuppetDB does with any of it.
//
// How to confirm: issue the recorded request against the deployed
// PuppetDB version with more matching resources than the configured
// result limit, and check that (a) it returns 200 with certname rows, and
// (b) two runs return the same first `result_limit` certnames.
func TestOutstanding_PuppetDBImpactEndpointAssumptions(t *testing.T) {
	t.Skip("requires a deployed PuppetDB; see this test's doc comment for the exact confirmation procedure")
}

// TestOutstanding_SensitiveWireShape is tasks.md task 12's third bullet.
//
// What must be confirmed against a rich-data-enabled compiler: that a
// Puppet `Sensitive` value serializes into a catalog as the Pcore
// generic-data object internal/diff/doc.go documents,
// `{"__ptype":"Sensitive","__pvalue":...}`. That shape was derived by
// reading Puppet's Ruby serializer source, not by capturing a live
// catalog response.
//
// What IS already covered, and why it is not enough:
// TestAcceptance_NoReportDisclosesSecretsOrManagedBytes serves exactly
// that shape and proves no artifact discloses the payload. But the
// fixture serves the assumed shape, so the test confirms PIACE redacts
// what it expects to see. If a real compiler emits a different encoding,
// this suite passes and the value is NOT redacted — the failure mode is
// silent disclosure, which is why this confirmation matters more than its
// one-line description suggests.
//
// How to confirm: compile a catalog containing a `Sensitive` parameter
// against the deployed compiler with rich data enabled, capture the
// response with `piace capture catalog`, and inspect the stored payload
// for the parameter's encoding.
func TestOutstanding_SensitiveWireShape(t *testing.T) {
	t.Skip("requires a rich-data-enabled compiler; see this test's doc comment for the exact confirmation procedure")
}
