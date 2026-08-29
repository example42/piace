# PIACE site policy notes

Handed to the inference service alongside the change, as context for the
assessment. Capped at 4000 bytes — anything past that is truncated, and the
truncation is recorded rather than hidden.

These are notes about *this estate*, not instructions to the model. Nothing
written here can change a comparison, an outcome, or an exit code: a risk
indication is a closed enum (`low`, `medium`, `high`, `unknown`) validated
locally, and the assessment never enters the result document.

## What this estate treats as high risk

- Any change to `sshd_config`, `sudoers`, or a PAM file. These lock people out
  of the estate before anyone notices, and the recovery path is console access.
- Removal of a `Service` resource, or a change to a service's `ensure`. A
  removed service is not stopped by Puppet — it is simply no longer managed,
  and stays running until something else reboots the host.
- Changes to `db-*` nodes during business hours. Replication is asynchronous
  and a restart is a customer-visible event.
- Anything touching `Firewall` or `nftables` resources on `lb-*`. These nodes
  terminate customer traffic.

## What this estate treats as low risk

- `File` content differences under `/etc/motd`, `/etc/issue`, or any path
  matching `/var/log/*` rotation configuration.
- Package version bumps within a maintained module's patch range, where the
  package is already managed and only `ensure` moves.
- Resource ordering or dependency-edge changes with no accompanying parameter
  change. They alter the run order, not the end state.

## Estate conventions worth knowing

- Roles are named in the certname: `web-*`, `app-*`, `db-*`, `lb-*`,
  `build-*`. A change reaching more than one role prefix is broader than most.
- Hiera data lives in `data/`; `data/common.yaml` reaches every node, and a
  change there is estate-wide by construction even when the diff looks small.
- `/srv/app/releases/*` churns on every deploy and is excluded from comparison
  in the target file. It should never appear in an assessment.
- The estate runs `noop` agents on `build-*`. A difference there is
  informational; nothing enforces it.

## What a useful review focus looks like here

Order by whether a human must be present when the change lands, not by how
many nodes it reaches. A one-line `sshd_config` change on two bastions
outranks a motd change on four hundred nodes.
