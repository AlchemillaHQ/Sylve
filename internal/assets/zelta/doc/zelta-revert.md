% zelta-revert(8) | System Manager's Manual

# NAME

**zelta revert** - rewind a ZFS dataset tree in place by renaming and cloning

# SYNOPSIS

**zelta revert** [_OPTIONS_] _endpoint_[@_snapshot_]

# DESCRIPTION
**zelta revert** rewinds a dataset to a previous snapshot state by renaming the current dataset and creating a clone from the specified snapshot. This technique is a non-destructive alternative to `zfs rollback`, preserving the current state for forensic analysis, testing, or recovery scenarios.

As with other Zelta commands, **zelta revert** works recursively on a dataset tree. The endpoint may be local or remote via **ssh(1)**.

After reverting, the original dataset remains accessible under a new name, allowing you to examine both the reverted state and the preserved current state.

## Revert Process

**zelta revert** performs these operations:

1. **Snapshot Selection**: If a `@snapshot` name is specified at the end of the endpoint, that snapshot is used. Otherwise, the most recent snapshot is selected.
2. **Dataset Preservation**: The target dataset is renamed by appending the snapshot name. For example, `pool/ds` reverted to snapshot `@yesterday` becomes `pool/ds_yesterday`. Note that no properties, including mountpoint and readonly status, are altered.
3. **Clone Creation**: For each child dataset, Zelta creates a clone from the selected snapshot at the original dataset path.

Remote endpoint names follow **scp(1)** conventions. Dataset names follow **zfs(8)** naming conventions.

Examples:

    Local:  pool/dataset
    Local:  pool/dataset@snapshot
    Remote: user@example.com:pool/dataset
    Remote: user@example.com:pool/dataset@snapshot

# OPTIONS

**Endpoint Argument (Required)**

_endpoint_
: The endpoint name to revert, optionally specifying a snapshot. If no snapshot is specified, the most recent snapshot is used.

**Output Options**

**-v, \--verbose**
: Increase verbosity. Specify once for operational detail, twice (`-vv`) for debug output.

**-q, \--quiet**
: Quiet output. Specify once to suppress warnings, twice (`-qq`) to suppress errors.

**-n, \--dryrun, \--dry-run**
: Display `zfs` commands without executing them.

# EXAMPLES

Revert a dataset to its most recent snapshot to investigate a bug while preserving the current state:

    zelta revert sink/source

The current dataset is renamed to `sink/source_<snapshot>` and a clone is created at the original path.

Revert a remote dataset to a specific snapshot:

    zelta revert production-host.example:sink/dataset@2025-12-31

After reverting a source dataset, rotate its backup targets to restore sync continuity:

    zelta revert sink/source/dataset
    zelta rotate sink/source/dataset backup-host.example:tank/target/dataset

The original diverged datasets remain accessible as `sink/source/dataset_<snapshot>` and `tank/target/dataset_<snapshot>`.

# EXIT STATUS
Returns 0 on success, non-zero on error.

# NOTES
See **zelta-options(7)** for environment variables, `zelta.env` configuration, and `zelta policy` integration.

# SEE ALSO
zelta(8), zelta-options(7), zelta-match(8), zelta-backup(8), zelta-policy(8), zelta-clone(8), zelta-rotate(8), ssh(1), zfs(8)

# AUTHORS
Daniel J. Bell <_bellhyve@zelta.space_>

# WWW
https://zelta.space
