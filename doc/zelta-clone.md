% zelta-clone(8) | System Manager's Manual

# NAME

**zelta clone** - perform a recursive clone operation

# SYNOPSIS

**zelta clone** [_OPTIONS_] _source_[@_snapshot_] _target_

# DESCRIPTION

**zelta clone** performs a recursive **zfs clone** operation on a dataset. This is useful for recursive duplication of dataset trees and backup inspection and recovery of files replicated with **zelta backup**. The clones will reference the latest or indicated snapshot, and consume practically no additional space. Clones can be modified and destroyed without affecting their origin datasets.

The _source_ and _target_ must be on the same host and pool. The mountpoint will be inherited below the target parent (as provided by **zfs clone**). The _target_ dataset must not exist. To create a clone on a remote host, ensure the _source_ and _target_ are identical including the username and hostname used.

Remote endpoint names follow **scp(1)** conventions. Dataset names follow **zfs(8)** naming conventions.

Example remote operation:

    zelta clone backup@host1.com:tank/zones/data backup@host1.com:tank/clones/data

# OPTIONS

**Endpoint Arguments (Required)**

_source_
: A dataset, in the form **pool[/dataset][@snapshot]**, which will be cloned along with all of its descendants. If a snapshot is not given, the most recent snapshot will be used.

_target_
: A dataset on the same pool as the _source_, where the clones will be created. This dataset must not exist.

**Output Options**

**-v, \--verbose**
: Increase verbosity. Specify once for operational detail, twice (`-vv`) for debug output.

**-q, \--quiet**
: Quiet output. Specify once to suppress warnings, twice (`-qq`) to suppress errors.

**-n, \--dryrun, \--dry-run**
: Display `zfs` commands without executing them.

**Snapshot Options**

**\--snapshot, \--snapshot-always**
: Ensure a snapshot before cloning.

**\--snap-name** _NAME_
: Specify snapshot name. Use `$(command)` for dynamic generation. Default: `$(date -u +zelta_%Y-%m-%d_%H.%M.%S)`.

**Dataset Options**

**-d, \--depth** _LEVELS_
: Limit recursion depth. For example, a depth of 1 includes only the specified dataset.

# EXAMPLES

Clone a dataset tree:

    zelta clone tank/vm/myos tank/temp/myos-202404

Recover a dataset tree, in place, to a previous snapshot's state:

    zfs rename tank/vm/myos tank/Archives/myos-202404
    zelta clone tank/Archives/myos-202404@goodsnapshot tank/vm/myos

Dry run to display the `zfs clone` commands without executing them:

    zelta clone -n tank/source/dataset tank/target/dataset

# EXIT STATUS

Returns 0 on success, non-zero on error.

# NOTES

See **zelta-options(7)** for environment variables and `zelta.env` configuration.

# SEE ALSO

zelta(8), zelta-options(7), zelta-backup(8), zelta-match(8), zelta-policy(8), zelta-revert(8), zelta-rotate(8), ssh(1), zfs(8), zfs-clone(8), zfs-promote(8)

# AUTHORS

Daniel J. Bell <_bellhyve@zelta.space_>

# WWW

https://zelta.space
