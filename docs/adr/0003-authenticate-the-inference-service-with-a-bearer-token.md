# Authenticate the inference service with a bearer token

requirements.md 3.5 states that PIACE authenticates exclusively via mTLS and
accepts no bearer tokens, and `internal/transport` enforces it beyond
configuration: `Client` deletes any `Authorization` header from every request
it sends, so a stolen `services.yaml` yields nothing usable. Every practical
OpenAI-compatible **inference service** authenticates with a bearer token, so
v0.2.0 records a scoped exception. The exception is scoped by construction
rather than by discipline: the inference client is its own package
(`internal/inference`) and is the only code in PIACE that sets an
`Authorization` header. `internal/transport` keeps stripping the header for
the compiler and PuppetDB, and v0.2.0 strengthened it while writing this
exception: it had stripped only on redirect, so the scoping this ADR rests on
was asserted rather than enforced. `Client.Do` now deletes the header
unconditionally on every request it sends. No existing caller set one, so
nothing changed behaviourally.

The token is never written in `services.yaml`. The `inference:` section accepts
`token_env:` (a variable name) or `token_file:` (a path), mirroring the
existing discipline that the services file holds references to credentials and
never credential material. `https` remains the only accepted scheme.

## Consequences

The `inference:` section lives in `services.yaml` alongside the compiler and
PuppetDB sections, and the three sections load independently: a
`services.yaml` containing only `inference:` is valid for `piace explain`,
which needs no mTLS identity and constructs no compiler or PuppetDB client.
A separate `--inference` file would have made that unreachability structural
rather than a property of the code, and was rejected to avoid a third
configuration file and a third command-line argument.
