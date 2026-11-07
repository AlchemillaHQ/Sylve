% zelta(8) | System Manager's Manual

# NAME
**zelta** - perform safe, recursive ZFS operations locally and remotely

# SYNOPSIS
        zelta -V | \--version | version
        zelta SUBCOMMAND [OPTIONS] [ARGUMENTS]

# DESCRIPTION
**zelta** provides tools for ZFS replication, backup verification, dataset recovery, and policy-based automation. All operations work recursively on dataset trees and support both local and remote execution via **ssh(1)**.

Remote dataset endpoints follow **scp(1)** conventions. Operations can be performed without installing **zelta** on remote systems—only standard ZFS utilities and SSH access are required.

Examples:

    Local:  pool/dataset@snapshot
    Remote: user@example.com:pool/dataset@snapshot
    IPv6:   user@[2001:db8::1]:pool/dataset

See **zfs(8)** for dataset naming conventions.

# SUBCOMMANDS
For detailed usage of each subcommand, run **zelta help <subcommand>** or see the respective manual page.

**zelta -?**
:    Display help message.

**zelta -V**, **\--version**, **version**
:    Display version information.

## Comparison

**zelta match** _source_ _target_
:    Compare two dataset trees and report matching snapshots or discrepancies. See **zelta-match(8)**.

## Backup

**zelta backup** _source_ _target_
:    Replicate a dataset tree. Creates snapshots if needed, detects optimal send options, and replicates intermediate snapshots. See **zelta-backup(8)**.

**zelta snapshot** _endpoint_
:    Create recursive snapshots on a local or remote endpoint. See **zelta-snapshot(8)**.

## Recovery

**zelta clone** _dataset_ _target_
:    Create a writable clone of a dataset tree. See **zelta-clone(8)**.

**zelta revert** _dataset_
:    Rewind a dataset to a previous snapshot by renaming and cloning. Preserves current state. See **zelta-revert(8)**.

**zelta rotate** _source_ _target_
:    Preserve divergent dataset versions through rename and clone operations. See **zelta-rotate(8)**.

**zelta failover** _source_ _target_
:    Lock an active source, perform a final backup, sync local properties, and unlock the promoted target. See **zelta-failover(8)**.

**zelta rebase** _upstream_ _target_
:    Rebase a dataset onto an upgraded upstream while preserving local files and incremental backup continuity. See **zelta-rebase(8)**.

**zelta lock** _endpoint_
:    Apply ordered dataset-tree readonly, canmount, unmount, and remount operations for promotion workflows. See **zelta-failover(8)**.

**zelta unlock** _endpoint_
:    Reverse **zelta lock** for a promoted or maintained dataset tree. See **zelta-failover(8)**.

**zelta propsync** _source_ _target_
:    Replay local ZFS properties from one dataset tree to another while preserving target-only local overrides. See **zelta-failover(8)**.

## Retention

**zelta prune** _endpoint_
:    Plan snapshot pruning without destroying data. See **zelta-prune(8)**.

**zprune** **--match-endpoint=**_guard_ _endpoint_
:    Validate and destroy snapshots selected by **zelta prune**. See **zprune(8)**.

## Automation

**zelta policy** [_options_]
:    Execute replication operations based on configuration file definitions. See **zelta-policy(8)**.

# OPTIONS AND ENVIRONMENT
Configuration follows a hierarchy from lowest to highest precedence:

    1. Internal defaults
    2. `~/.config/zelta/zelta.env` if present, otherwise `/usr/local/etc/zelta/zelta.env`
    3. Policy configuration (`zelta.conf`)
    4. Environment variables
    5. Command-line arguments

See **zelta-options(7)** for details.

# FILES
**~/.config/zelta/zelta.conf**
:    User policy configuration file, used by default when present.

**/usr/local/etc/zelta/zelta.conf**
:    System policy configuration file, used when user configuration is absent.

**~/.config/zelta/zelta.env**
:    User default setting overrides, used by default when present.

**/usr/local/etc/zelta/zelta.env**
:    System default setting overrides, used when user configuration is absent.

**~/.local/share/zelta/doc**
:    User-installed Zelta manual root, containing `man7` and `man8` directories.

**/usr/local/man**
:    System manual root used by root installs.

# EXAMPLES
The following examples use "sink" as the source pool and "tank" as the backup target.

Replicate a dataset to a remote host:

    zelta backup sink/data/project backupuser@backup-host.example:tank/backups/project

Compare local and remote dataset trees:

    zelta match sink/data remote:tank/backups

Revert a dataset to its previous snapshot:

    zelta revert sink/data/project

Update a target that has diverged:

    zelta rotate sink/data/project backupuser@backup-host.example:tank/backups/project

Back up everything defined in policy settings (**zelta.conf**).

    zelta policy

# EXIT STATUS
Returns 0 on success, non-zero on error.

# BUGS
When using Original Awk, Zelta cannot reliably obtain command exit codes from pipes. A failed or interrupted command can produce a partial dataset or snapshot listing that is mistaken for a complete list. This general limitation affects **zelta match** and operations that consume its output; some listing paths do not check completion even with other AWK implementations.

The whole-tree preflight for **zelta revert** checks an explicit completion status and rejects failed or interrupted listings before renaming datasets. This check does not resolve the general listing-truncation limitation elsewhere.

# NOTES

See **zelta-options(7)** for environment variables and `zelta.env` configuration.

**zelta sync** remains available for compatibility as an alias for **zelta backup -i**. New documentation uses explicit **zelta backup** commands.

# SEE ALSO
zelta-match(8), zelta-backup(8), zelta-policy(8), zelta-clone(8), zelta-options(7), zelta-prune(8), zprune(8), zelta-rebase(8), zelta-failover(8), zelta-revert(8), zelta-rotate(8), zelta-snapshot(8), cron(8), ssh(1), zfs(8)

# AUTHORS
Daniel J. Bell <_bellhyve@zelta.space_>

# WWW
https://zelta.space
