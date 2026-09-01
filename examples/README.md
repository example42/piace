# Example configuration

Realistic, loadable configuration for a small mixed estate: web, app, db,
load-balancer and build nodes behind `puppet.ops.example.com`. Every file here
passes PIACE's strict loader: unknown keys are rejected, so a typo is a load
error rather than a silently ignored setting.

Copy the pair that matches your situation, change the endpoints, certnames and
TLS paths, and delete the comments you no longer need.

## Which files

| File | Used by | For |
| --- | --- | --- |
| [`services.yaml`](services.yaml) | all subcommands | **Start here.** Endpoints and credential references for the whole pipeline |
| [`targets-puppetdb-baseline.yaml`](targets-puppetdb-baseline.yaml) | `compare` | **Start here.** v4 against PuppetDB's latest catalog; nothing written server-side |
| [`targets-snapshot-baseline.yaml`](targets-snapshot-baseline.yaml) | `compare`, `capture` | Frozen baseline captured to disk; required with v3 |
| [`targets-v3-legacy.yaml`](targets-v3-legacy.yaml) | `compare` | A compiler with no v4 endpoint. **Read the header before copying** |
| [`services-explain-only.yaml`](services-explain-only.yaml) | `explain` | A services file may carry only the sections a command needs |
| [`change-context.yaml`](change-context.yaml) | `explain` | The repository change under test |
| [`policy-notes.md`](policy-notes.md) | `explain` | Site policy handed to the model as context |
| [`ci/`](ci/) | `compare`, `explain` | Working GitHub Actions, GitLab CI and Azure Pipelines definitions |

Two files, deliberately separate: the reviewable selection and policy file
(`targets-*.yaml`), and the endpoint file naming where credentials come from
(`services.yaml`).

## Running them

```sh
# 1. Compare against PuppetDB's latest catalog: the supported path
piace compare --targets examples/targets-puppetdb-baseline.yaml \
              --services examples/services.yaml \
              --json-out report.json --html-out report.html

# 2. Or freeze the baseline first, then compare against it
piace capture catalog --targets examples/targets-snapshot-baseline.yaml \
                      --services examples/services.yaml \
                      --environment production --replace
piace compare --targets examples/targets-snapshot-baseline.yaml \
              --services examples/services.yaml

# 3. Optionally assess the stored result document
export PIACE_INFERENCE_TOKEN=...
piace explain --json-in report.json \
              --services examples/services.yaml \
              --change examples/change-context.yaml \
              --ai-out assessment.json --html-out report.html
```

These will not run as-is: the endpoints and certnames are fictional and the
TLS paths do not exist. They load, which is the part these files are for.

## Two things that bite

- **`capture` takes no destination flag.** It writes to `baseline.file` or
  `facts.file`, and skips with a warning any target whose matching `source` is
  not `file`. Configure the file source before the capture that populates it.
- **v3 rewrites PuppetDB state, and PIACE will not stop you.**
  `catalog_api: v3` with `baseline.source: puppetdb` loads, looks normal, and
  corrupts the baseline it just read. See
  [`targets-v3-legacy.yaml`](targets-v3-legacy.yaml).

## One rule about paths

Every relative path named in a config file resolves against the directory of
the file that names it. `baseline.file` and `facts.file` resolve against the
target file; the TLS paths, `token_file` and `policy_notes_file` resolve
against the services file. Nothing resolves against the working directory, so
moving a file takes its paths with it.

The CI form skips paths in the file entirely: `ca_bundle_env`,
`client_cert_env` and `private_key_env` name an environment variable holding
an absolute path, so a committed services file is read in place, unmodified,
by a job whose credential directory did not exist when the file was written.

## In a pipeline

[`ci/`](ci/) holds the same configuration arranged for CI: two jobs, so the
identity that reads every catalog in the estate is never in the same job as
the inference token, with the identity written to a per-job directory outside
the checkout and reached through `*_env`.

Read [docs/ci.md](../docs/ci.md) alongside them: it covers where each file
belongs, how the exit code becomes a gate, and what changes when the runner is
one you do not control.

Full reference: the [README](../README.md).
