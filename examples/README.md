# Example configuration

Realistic, loadable configuration for a small mixed estate: web, app, db,
load-balancer and build nodes behind `puppet.ops.example.com`. Every file here
passes PIACE's strict loader — unknown keys are rejected, so a typo is a load
error rather than a silently ignored setting.

Copy the pair that matches your situation, change the endpoints, certnames and
TLS paths, and delete the comments you no longer need.

## Which files

| File | Used by | For |
| --- | --- | --- |
| [`services.yaml`](services.yaml) | `compare`, `capture` | Compiler and PuppetDB endpoints, mTLS identity |
| [`targets-puppetdb-baseline.yaml`](targets-puppetdb-baseline.yaml) | `compare` | **Start here.** v4 against PuppetDB's latest catalog; nothing written server-side |
| [`targets-snapshot-baseline.yaml`](targets-snapshot-baseline.yaml) | `compare`, `capture` | Frozen baseline captured to disk; required with v3 |
| [`targets-v3-legacy.yaml`](targets-v3-legacy.yaml) | `compare` | A compiler with no v4 endpoint. **Read the header before copying** |
| [`services-with-inference.yaml`](services-with-inference.yaml) | `compare`, `explain` | One file for the whole pipeline, inference section included |
| [`services-explain-only.yaml`](services-explain-only.yaml) | `explain` | Assessment on a runner with no Puppet mTLS material |
| [`change-context.yaml`](change-context.yaml) | `explain` | The repository change under test |
| [`policy-notes.md`](policy-notes.md) | `explain` | Site policy handed to the model as context |

Two files, deliberately separate: the reviewable selection/policy file
(`targets-*.yaml`), and the endpoint/mTLS file that does not belong in a review
diff (`services*.yaml`).

## Running them

```sh
# 1. Compare against PuppetDB's latest catalog — the supported path
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
              --services examples/services-with-inference.yaml \
              --change examples/change-context.yaml \
              --ai-out assessment.json --html-out report.html
```

These will not run as-is: the endpoints and certnames are fictional and the
TLS paths do not exist. They load, which is the part these files are for.

## Three things that bite

- **Path resolution differs per file.** TLS paths in `services.yaml` resolve
  against the **process working directory** — use absolute paths.
  `baseline.file` and `facts.file` in a target file resolve against the
  **target file's directory**. `policy_notes_file` resolves against the
  **services file's directory**. All three happen to coincide if you run from
  the repository root with everything in `examples/`, which is exactly the
  accident that misleads someone who copies one file elsewhere.
- **`capture` takes no destination flag.** It writes to `baseline.file` or
  `facts.file`, and skips with a warning any target whose matching `source` is
  not `file`. Configure the file source before the capture that populates it.
- **v3 rewrites PuppetDB state, and PIACE will not stop you.**
  `catalog_api: v3` with `baseline.source: puppetdb` loads, looks normal, and
  corrupts the baseline it just read. See
  [`targets-v3-legacy.yaml`](targets-v3-legacy.yaml).

Full reference: the [README](../README.md).
