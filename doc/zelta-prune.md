% zelta-prune(8) | System Manager's Manual

# NAME

**zelta prune** - identify snapshots safe for deletion based on backup state

# SYNOPSIS

**zelta prune** [_OPTIONS_] _source_ _target_

# DESCRIPTION

**zelta prune** identifies snapshots on a source dataset that have been safely replicated to a target and are eligible for deletion based on retention policies. This command is useful for managing snapshot accumulation on production systems while ensuring backup integrity.

**zelta prune** only suggests snapshots for deletion—it does not delete them. Output is provided in a format suitable for review before execution.

As with other Zelta commands, **zelta prune** works recursively on dataset trees. Both source and target may be local or remote via **ssh(1)**.

## Safety Criteria

**WARNING:** `zelta prune` is a new feature currently in production testing and will be formally released in Zelta 1.2 (May 2026).

A snapshot is considered safe to prune only if:

1. The snapshot exists on the target (has been replicated)
2. The snapshot is older than the most recent common match point
3. The snapshot meets the minimum retention requirements

Snapshots newer than the common match point are never suggested for deletion, as they may be needed for future incremental replication.

Remote endpoint names follow **scp(1)** conventions. Dataset names follow **zfs(8)** naming conventions.

# OPTIONS

**Endpoint Arguments (Required)**

_source_
: The dataset tree containing snapshots to evaluate for pruning.

_target_
: The backup dataset tree used to verify replication status.

**Output Options**

**-v, \--verbose**
: Increase verbosity. Specify once for operational detail, twice (`-vv`) for debug output.

**-q, \--quiet**
: Quiet output. Specify once to suppress warnings, twice (`-qq`) to suppress errors.

**-n, \--dryrun, \--dry-run**
: Display `zfs` commands without executing them.

**Retention Options**

**\--keep-snap-num** _N_
: Minimum number of snapshots to keep after the match point. Default: 100.

**\--keep-snap-days** _N_
: Minimum age in days before a snapshot is eligible for deletion. Default: 90.

**\--no-ranges**
: Disable range compression in output. By default, consecutive snapshots are displayed as ranges (e.g., `snap1%snap5`). This option outputs individual snapshot names, one per line.

**Dataset Options**

**-d, \--depth** _LEVELS_
: Limit recursion depth. For example, a depth of 1 includes only the specified dataset.

**-X, \--exclude** _PATTERN_
: Exclude datasets matching the specified pattern. See **zelta-options(7)** for pattern syntax.

# OUTPUT FORMAT

By default, **zelta prune** outputs snapshot ranges using ZFS range syntax:

    pool/dataset@oldest_snap%newest_snap

This format is compatible with `zfs destroy` for batch deletion. With `--no-ranges`, individual snapshot names are output one per line.

# EXAMPLES

Identify prunable snapshots with default retention (100 snapshots, 90 days):

    zelta prune tank/data backup-host.example:tank/backups/data

Use stricter retention (keep 200 snapshots, 180 days minimum age):

    zelta prune --keep-snap-num=200 --keep-snap-days=180 \
        tank/data backup-host.example:tank/backups/data

Output individual snapshots instead of ranges:

    zelta prune --no-ranges tank/data backup-host.example:tank/backups/data

Review prunable snapshots, then delete after confirmation:

    # First, review what would be deleted
    zelta prune tank/data backup:tank/backups/data

    # Save to a file for review
    zelta prune tank/data backup:tank/backups/data | tee /tmp/prune-list.txt

    # Review the list carefully
    # Use `zelta prune -v` to see matching snapshots being kept

    # If satisfied, delete the replicated source snapshots
    cat /tmp/prune-list.txt | xargs -n1 zfs destroy

Exclude temporary datasets from consideration:

    zelta prune -X '*/tmp' tank/data backup:tank/backups/data

# EXIT STATUS

Returns 0 on success, non-zero on error.

# NOTES

**zelta prune** is experimental. Always review output before executing deletions. Deleting snapshots is irreversible.

This command is driven by the **zelta match** comparison engine. See **zelta-match(8)** for details on how source and target snapshots are compared.

See **zelta-options(7)** for environment variables and `zelta.env` configuration.

# SEE ALSO

zelta(8), zelta-options(7), zelta-match(8), zelta-backup(8), zfs(8), zfs-destroy(8)

# AUTHORS

Daniel J. Bell <_bellhyve@zelta.space_>

# WWW

https://zelta.space
