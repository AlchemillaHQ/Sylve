% zelta-rebase(8) | System Manager's Manual

# NAME
**zelta rebase** - rebase a dataset onto an upgraded upstream while preserving local files

# SYNOPSIS
**zelta rebase** [_OPTIONS_] _upstream_ _target_

# DESCRIPTION
**zelta rebase** replaces a production dataset with a new dataset based on an upgraded upstream while preserving incremental backup continuity and local files.

The old target is automatically renamed out of the way using its clone origin snapshot name, and the new upstream is received at the original target name. If a preserve file is found, specified local files are copied from the old target into the new one.

This is useful for image, template, appliance, and development workflows where local changes must move forward onto a newer upstream base without forcing a full backup history restart.

# OPERANDS

_upstream_
: Upgraded base dataset. If a snapshot is specified (e.g., _pool/dataset@snap_), that snapshot is used; otherwise the latest snapshot is used.

_target_
: Current production dataset to rebase.

# OPTIONS

**\--rebase-file** _file_
: Override the default preserve file path. If not specified, **zelta rebase** looks for _.zelta-rebase.preserve_ in the upstream dataset's mountpoint.

**-n**, **\--dryrun**, **\--dry-run**
: Display underlying commands without executing them.

**-v**, **\--verbose**
: Increase verbosity. Specify once for operational detail, twice for debug output.

**-q**, **\--quiet**
: Decrease log output.

# PRESERVE FILE

The upstream dataset may contain a _.zelta-rebase.preserve_ file listing paths relative to the dataset mountpoint to copy from the old target into the new target.

Resolution order:

1. **\--rebase-file** _file_ (CLI override)
2. _.zelta-rebase.preserve_ in the upstream dataset's mountpoint
3. Neither found — no files are preserved; rebase behaves like a rotate that preserves mountability

# EXAMPLES

Rebase a production dataset onto the latest upstream snapshot:

    zelta rebase apool/jails/base cpool/jails/web01

Rebase onto a specific snapshot:

    zelta rebase apool/jails/base@freebsd14.2-p1 cpool/jails/web01

Use an external preserve file:

    zelta rebase --rebase-file /etc/jails/web01.preserve apool/jails/base cpool/jails/web01

# EXIT STATUS
Returns 0 on success, non-zero on error.

# NOTES

The old target is renamed to _target_\__origin-snapshot_, where _origin-snapshot_ is the name of the snapshot from which the target was originally cloned.

Use **zelta match** before and after rebasing to confirm lineage and backup state.

# SEE ALSO
zelta(8), zelta-backup(8), zelta-match(8), zelta-clone(8), zelta-rotate(8), zelta-options(7), ssh(1), zfs(8)

# AUTHORS
Daniel J. Bell <_bellhyve@zelta.space_>

# WWW
https://zelta.space
