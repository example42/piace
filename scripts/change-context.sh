#!/usr/bin/env bash
#
# Generate a change context file for `piace explain --change`.
#
# PIACE never invokes git. It reads a file the caller produces, which is
# what keeps the tool a client of PuppetDB and a compiler and nothing
# else — and what lets a CI system that has no checkout, or a different
# VCS entirely, still describe its change.
#
# Usage:
#   scripts/change-context.sh BASE_REF [HEAD_REF] > change.yaml
#   piace explain --json-in report.json --services services.yaml \
#     --change change.yaml --ai-out assessment.json --html-out report.html
#
# Commit *subjects* are emitted, never bodies. A commit body is free text
# of unbounded length written by whoever pushed, and it is the part of a
# repository most likely to carry a customer name, a ticket paste, or a
# credential someone meant to delete. `piace explain` refuses a `body`
# key outright, so this is enforced at both ends.
#
# Title and description are left to the caller: they are usually a pull
# request's, which git does not have. PIACE caps both.

set -euo pipefail

base_ref=${1:?usage: change-context.sh BASE_REF [HEAD_REF]}
head_ref=${2:-HEAD}

# yaml_scalar emits a double-quoted YAML scalar, escaping the two
# characters that can end it. Commit subjects are arbitrary text.
yaml_scalar() {
  printf '"%s"' "$(printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')"
}

merge_base=$(git merge-base "$base_ref" "$head_ref")

printf 'version: 1\n'
printf 'change:\n'
printf '  base_ref: %s\n' "$(yaml_scalar "$base_ref")"
printf '  head_ref: %s\n' "$(yaml_scalar "$(git rev-parse --abbrev-ref "$head_ref")")"

printf '  commits:\n'
while IFS=$'\t' read -r sha subject author; do
  [ -n "$sha" ] || continue
  printf '    - sha: %s\n' "$(yaml_scalar "$sha")"
  printf '      subject: %s\n' "$(yaml_scalar "$subject")"
  printf '      author: %s\n' "$(yaml_scalar "$author")"
done < <(git log --format=$'%H\t%s\t%an' "$merge_base..$head_ref")

printf '  changed_paths:\n'
while IFS= read -r path; do
  [ -n "$path" ] || continue
  printf '    - %s\n' "$(yaml_scalar "$path")"
done < <(git diff --name-only "$merge_base" "$head_ref")
