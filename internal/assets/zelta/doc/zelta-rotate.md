% zelta-rotate(8) | System Manager's Manual

# NAME

**zelta rotate** - recover sync continuity by renaming, cloning, and incrementally syncing a ZFS replica

# SYNOPSIS

**zelta rotate** [_OPTIONS_] _source_ _target_

# DESCRIPTION
**zelta rotate** renames a target replica and performs a multi-way clone and sync operation to restore sync continuity when a source and target have diverged. The operation considers up to four dataset states: the current source, the current target, the source's origin (if cloned), and the target's origin, finding the optimal sync path between them.

This technique is a non-destructive alternative to `zfs rollback` and `zfs recv -F`, useful for maintaining forensic evidence in recovery scenarios and advanced iterative infrastructure workflows.

As with `zelta backup`, `zelta rotate` works recursively on dataset trees. Both source and target may be local or remote via **ssh(1)**.

The rotate operation syncs the minimum data necessary to complete the task, targeting the next available snapshot for incremental replication. After successful rotation, run `zelta backup` with your preferred settings to ensure full consistency.

## Rotation Process

**zelta rotate** performs these operations:

1. **Snapshot Creation**: If the source has uncommitted changes, a new snapshot is created (unless `--no-snapshot` is specified).
2. **Match Detection**: Zelta searches for common snapshots between source and target. If none are found, it checks the source origin (the dataset from which the source was cloned).
3. **Target Preservation**: The target is renamed by appending the matching snapshot name. For example, `pool/ds` with matching snapshot `@yesterday` becomes `pool/ds_yesterday`. Note that no properties, including mountpoint and readonly status, are altered.
4. **Incremental Sync**: For each child dataset with a matching snapshot, Zelta creates a clone at the target and performs an incremental sync.
5. **Full Backup**: For child datasets without matching snapshots, a full backup is performed.

## Limitations

If the topmost target dataset has no common snapshot with either the source or source origin, `zelta rotate` will not continue. In this case, manually rename the diverged target and use `zelta backup` to perform a full replication.

Remote endpoint names follow **scp(1)** conventions. Dataset names follow **zfs(8)** naming conventions. The target must be a replica of the source.

Examples:

    Local:  pool/dataset
    Remote: user@example.com:pool/dataset

# OPTIONS

**Endpoint Arguments (Required)**

If both endpoints are remote, the default behavior is **pull replication** (`--pull`). This requires that the target user have ssh access to the source, typically provided by ssh keys or agent forwarding. For advanced ssh configuration, see _https://zelta.space_.

_source_
: The dataset to replicate. The source origin, if needed, is automatically detected.

_target_
: The replica dataset to be rotated.

**Output Options**

**-v, \--verbose**
: Increase verbosity. Specify once for operational detail, twice (`-vv`) for debug output.

**-q, \--quiet**
: Quiet output. Specify once to suppress warnings, twice (`-qq`) to suppress errors.

**-n, \--dryrun, \--dry-run**
: Display `zfs` commands without executing them.

**Connection Options**

**\--push, \--pull, \--sync-direction** _DIRECTION_
: When both endpoints are remote, use `PULL` (default) or `PUSH` sync direction.

**Snapshot Options**

**\--no-snapshot**
: Do not create snapshots. If a snapshot is needed for rotation, the operation will fail.

**\--snapshot**
: Force snapshot creation even if the source has no uncommitted changes.

**\--snap-name** _NAME_
: Specify snapshot name. Use `$(command)` for dynamic generation. Default: `$(date -u +zelta_%Y-%m-%d_%H.%M.%S)`.

# EXAMPLES

A common workflow after accidentally diverging a source from its backup:

First, rewind the source to its previous snapshot state using a rename and clone operation:

    zelta revert sink/source/dataset

Then rotate the target replica, performing a 4-way incremental sync to match the reverted source:

    zelta rotate sink/source/dataset backup-host.example:tank/target/dataset

Finally, ensure full consistency between source and target:

    zelta backup sink/source/dataset backup-host.example:tank/target/dataset

The original diverged datasets remain accessible as `sink/source/dataset_<snapshot>` and `tank/target/dataset_<snapshot>` for forensic analysis.

# EXIT STATUS
Returns 0 on success, non-zero on error.

# NOTES
See **zelta-options(7)** for environment variables, `zelta.env` configuration, and `zelta policy` integration.

# SEE ALSO
zelta(8), zelta-options(7), zelta-match(8), zelta-backup(8), zelta-policy(8), zelta-clone(8), zelta-revert(8), ssh(1), zfs(8), zfs-allow(8)

# AUTHORS
Daniel J. Bell <_bellhyve@zelta.space_>

# WWW
https://zelta.space
