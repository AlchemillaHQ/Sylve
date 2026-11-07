% zelta-match(8) | System Manager's Manual

# NAME

**zelta match** - compare a ZFS dataset tree with its replica

# SYNOPSIS

**zelta match** [_OPTIONS_] _source_ _target_

# DESCRIPTION

**zelta match** recursively displays a dataset and its children, the _source_, and compares it to its replica, the _target_. **zelta match** displays fields describing differences and similarities between the two dataset trees. This is useful for assisting with replication operations and confirming backups.

# OPTIONS

**Endpoint Arguments (Required)**

_source_
: Dataset tree to compare.

_target_
: Replica dataset tree to compare against the source.

**Logging Options**

**-v, \--verbose**
: Increase verbosity. Specify once for operational detail, twice (`-vv`) for debug output.

**-q, \--quiet**
: Decrease log level. Specify once to suppress notices, twice (`-qq`) to suppress warnings.

**\--log-level** _LEVEL_
: Specify a log level value 0-4: errors (0), warnings (1), notices (2, default), info (3, verbose), and debug (4).

**\--log-mode** _MODE_
: Enable the specified log mode. Currently supported: `text` and `json`.

**\--text**
: Force default output notices to print as plain text standard output.

**-n, \--dryrun, \--dry-run**
: Display `zfs` commands without executing them.

**Dataset and Snapshot Options**

**-d, \--depth** _LEVELS_
: Limit recursion depth. For example, a depth of 1 includes only the specified dataset.

**-X, \--exclude** _PATTERN_
: Exclude datasets or source snapshots matching the specified pattern. This option can include multiple patterns separated by commas and can be specified multiple times. See _INCLUDE AND EXCLUDE PATTERNS_ in **zelta-options(7)** for details.

**\--include** _PATTERN_
: Include only datasets or source snapshots matching the specified pattern. This option can include multiple patterns separated by commas and can be specified multiple times. See _INCLUDE AND EXCLUDE PATTERNS_ in **zelta-options(7)** for details.

**Columns and Summary Behavior**

**-H**
: Suppress column headers and separate columns with a single tab.

**-p**
: Output sizes in exact numbers instead of human-readable values like `1M`.

**-o** _FIELD_[,...]
: Specify a list of **zelta match** columns. See _FIELD OPTIONS_ below for detail.

**\--written**
: Calculate data sizes for datasets and snapshots. Enabled by default, but it can impact list time.

**\--time**
: Calculate the time of each `zfs list` operation.

# FIELD OPTIONS

| FIELD       | DESCRIPTION                                 |
|-------------|---------------------------------------------|
| ds_suffix   | Relative dataset name                       |
| match       | Latest matching snapshot                    |
| num_matches | Total number of matches                     |
| xfer_size   | Unsynced data size based on snapshots       |
| xfer_num    | Unsynced snapshot count                     |
| src_name    | Full source dataset name                    |
| src_first   | First source snapshot                       |
| src_next    | Next source snapshot after match            |
| src_last    | Latest source snapshot                      |
| src_written | Source data written since last snap         |
| src_snaps   | Total source snapshots                      |
| tgt_name    | Full target dataset name                    |
| tgt_first   | First target snapshot                       |
| tgt_next    | Next target snapshot that is blocking sync  |
| tgt_last    | Latest target snapshot                      |
| tgt_written | Target data written since last snap         |
| tgt_snaps   | Total target snapshots                      |
| info        | Sync state description                      |

# EXAMPLES

Basic comparison between local source and target datasets:

    zelta match tank/source/dataset tank/target/dataset

Remote comparison showing the size in bytes of missing snapshots on the second system:

    zelta match user@remote.host1:tank/source/dataset user2@remote.host2:tank/target/dataset

Quick backup integrity check—compare the top two levels of similar backup repositories to see which backups might be missing or out of sync between each host:

    zelta match -d2 backuphost:rust101/Backups rust000/Backups

Dry run to display the `zfs list` commands that would be used without executing them:

    zelta match -n tank/source/dataset tank/target/dataset

# EXIT STATUS

Returns 0 on success, non-zero on error.

# NOTES

See **zelta-options(7)** for environment variables and `zelta.env` configuration.

# SEE ALSO
zelta(8), zelta-backup(8), zelta-policy(8), zelta-clone(8), zelta-options(7), zelta-revert(8), zelta-rotate(8), zelta-snapshot(8), cron(8), ssh(1), zfs(8)

# AUTHORS
Daniel J. Bell <_bellhyve@zelta.space_>

# WWW
https://zelta.space
