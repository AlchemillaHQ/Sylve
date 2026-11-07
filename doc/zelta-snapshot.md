% zelta-snapshot(8) | System Manager's Manual

# NAME

**zelta snapshot** - create recursive ZFS snapshots locally or remotely

# SYNOPSIS

**zelta snapshot** [_OPTIONS_] _endpoint_[@_snapshot_]

# DESCRIPTION

**zelta snapshot** creates a recursive snapshot on a dataset tree. The endpoint may be local or remote via **ssh(1)**.

As with other Zelta commands, **zelta snapshot** works recursively on a dataset tree. This provides a simple way to create consistent, atomic snapshots across an entire dataset hierarchy without requiring Zelta to be installed on remote systems.

Remote endpoint names follow **scp(1)** conventions. Dataset names follow **zfs(8)** naming conventions.

Examples:

    Local:  pool/dataset
    Local:  pool/dataset@my-snapshot
    Remote: user@example.com:pool/dataset
    Remote: user@example.com:pool/dataset@backup-2025-01-15

# OPTIONS

**Endpoint Argument (Required)**

_endpoint_
: The dataset to snapshot. If a snapshot name is specified with `@snapshot`, that name is used. Otherwise, the name is determined by the `--snap-name` option.

**Output Options**

**-v, \--verbose**
: Increase verbosity. Specify once for operational detail, twice (`-vv`) for debug output.

**-q, \--quiet**
: Decrease log level. Specify once to suppress notices, twice (`-qq`) to suppress warnings.

**-n, \--dryrun, \--dry-run**
: Display `zfs` commands without executing them.

**Snapshot Options**

**\--snap-name** _NAME_
: Specify snapshot name. Use `$(command)` for dynamic generation. Default: `$(date -u +zelta_%Y-%m-%d_%H.%M.%S)`. This option is ignored if a snapshot name is provided in the endpoint argument.

**\--snap-prefix** _PREFIX_
: Prefix generated snapshot names. If the resolved name starts with `zelta`, _PREFIX_ replaces `zelta`; otherwise _PREFIX_ is prepended verbatim. This option is ignored if a snapshot name is provided in the endpoint argument.

**\--snap-time** _TIME_
: Skip snapshot creation unless the newest existing snapshot is older than _TIME_. Bare numbers are seconds; suffixes such as `h`, `d`, `w`, and `y` are accepted.

**\--snap-size** _SIZE_
: Skip snapshot creation unless the dataset tree has at least _SIZE_ of written data since the newest existing snapshot. _SIZE_ accepts ZFS-style byte counts and suffixes.

**Dataset Options**

**-d, \--depth** _LEVELS_
: Limit recursion depth. For example, a depth of 1 includes only the specified dataset.

# EXAMPLES

Create a snapshot with the default naming scheme:

    zelta snapshot tank/data

Create a snapshot with a specific name:

    zelta snapshot tank/data@before-upgrade

Create a snapshot on a remote host:

    zelta snapshot backup@storage.example.com:tank/backups

Create a snapshot with a custom naming scheme:

    zelta snapshot --snap-name "manual_$(date +%Y%m%d)" tank/data

Create a snapshot only when the newest existing snapshot is at least six hours old:

    zelta snapshot --snap-time 6h tank/data

Dry run to preview the command:

    zelta snapshot -n tank/data

# EXIT STATUS

Returns 0 on success, non-zero on error.

# NOTES

See **zelta-options(7)** for environment variables and `zelta.env` configuration.

# SEE ALSO

zelta(8), zelta-options(7), zelta-backup(8), zfs(8), zfs-snapshot(8)

# AUTHORS

Daniel J. Bell <_bellhyve@zelta.space_>

# WWW

https://zelta.space
