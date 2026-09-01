# Keep the change assessment out of the result document

PIACE's result document is canonically encoded and `schema_version`-tagged so
that identical input catalogs and configuration produce byte-identical
artifacts, and `cmd/piace/acceptance_determinism_test.go` asserts exactly that.
A model-generated **change assessment** cannot hold that property: even with
sampling pinned, a provider-side model revision changes the bytes.
Rather than weaken the invariant to accommodate an advisory feature, v0.2.0
quarantines the assessment into a separate artifact with its own independent
`ai_schema_version`, carrying a SHA-256 checksum of the canonical result
document it was derived from. `Result.SchemaVersion` stays `1` and the v0.1.0
acceptance suite passes unmodified.

## Considered Options

Embedding the assessment in `Result` and bumping `schema_version` to `2` was
rejected because it would require rewriting the determinism test to exclude a
subtree — turning a guarantee a reader can state in one sentence into one with
an exception list. Claiming determinism via a response cache keyed on the
result checksum was rejected because a cache miss still produces
nondeterminism, which is a guarantee that holds only when it happens to hold.

## Consequences

Because the assessment is a pure function of a stored result document, it is
produced by a second command (`piace explain`) rather than inside `compare`.
That keeps `compare`'s configuration surface, dependency surface, failure
modes, and service reach unchanged, and it makes the whole feature runnable —
and testable — offline against a report produced by some earlier run. The cost
is one extra step in a CI pipeline.
