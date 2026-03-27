% zelta-options(7) | System Manager's Manual

# NAME
**zelta-options** - environment and policy options for Zelta behavior

# SYNOPSIS
Environment variables and policy configuration options.

# DESCRIPTION
Zelta's behavior can be modified through environment variables, command-line arguments, and policy configuration files. This manual documents all available options and their effects.

Options are set differently based on context:

- **Shell environment**: Environment variables must be prefixed with `ZELTA_` (e.g., `ZELTA_DEPTH=2`)
- **Environment file**: In `zelta.env`, use `KEY=value` pairs (e.g., `DEPTH=2`)
- **Policy file**: `zelta policy` additionally uses the YAML-like `zelta.conf` for granular settings per backup job with `KEY: value` pairs (e.g., `DEPTH: 1`)
- **Command-line arguments**: Options map to double-dash arguments (e.g., `--depth`)

For on/off variable assignments, use "1" for true and "0" for false.

## Option Hierarchy

Options follow an override hierarchy to provide flexibility in all contexts.

1. **Defaults** - Built-in defaults in the `zelta` controller script
2. **`zelta.env`** - System-wide environment file (default: `/usr/local/etc/zelta/zelta.env`)
3. **`zelta.conf`** - Policy configuration file (`zelta policy` only, default: `/usr/local/etc/zelta/zelta.conf`)
4. **Environment variables** - User environment (must prefix names with `ZELTA_`)
5. **Command-line arguments** - Highest priority, overrides all other sources

For example, running `zelta policy --no-snapshot` will ensure the all configured backups will run without taking snapshots regardless of snapshot configuration in other contexts.

# SETUP AND ENVIRONMENT-ONLY OPTIONS
The following options should be modified in the environment to ensure proper installation and startup of the `zelta` script. Typically, these should be defined in the user's shell rc script. In particular, `ZELTA_AWK` and `ZELTA_ENV` will be used prior to loading `zelta.env` so they must be exported beforehand.

**ZELTA_AWK**
:   The **awk** executable. The default is the awk in the path. Example: `ZELTA_AWK='mawk -Wi'`.

**ZELTA_SHARE**
:   The location of Zelta assets including the AWK scripts and data files. The default is `/usr/local/share/zelta`.

**ZELTA_ETC**
:   The location of `zelta.env` and `zelta.conf`. The default is `/usr/local/etc/zelta`.

**ZELTA_ENV**
:   The exact path of `zelta.env`.

**ZELTA_DOC**
:   The location of Zelta's manpages. Default is unset, using the system-wide manual.

# LOGGING OPTIONS

**LOG_FILE**
:   Divert all output into the indicated file.

**LOG_LEVEL**
:   Specify a log level value 0-4: errors (0), warnings (1), notices (2, default), info (3, verbose), and debug (4).

**LOG_MODE**
:   Enable the specified log modes. Currently supported: 'text' (default) and 'json' (`zelta backup` related verbs only).

# JSON OUTPUT OPTIONS

**SYSTIME**
:   Override the system time function for JSON timestamps. Required for mawk users, as mawk's `srand()` does not reliably return epoch time. Set to `date +%s` for compatibility. Not needed with gawk or original-awk (nawk/bwk).

    Example: `ZELTA_SYSTIME='date +%s'`

# SSH OPTIONS

**REMOTE_COMMAND**
:   The remote shell command. Defaults to `ssh`.

**REMOTE_DEFAULT**
:   The default remote shell command used for misc operations which should prevent reading from stdin. Defaults to `REMOTE_COMMAND -n` (`ssh -n`).

**REMOTE_SEND**
:   The remote shell command used for `zfs send`. Defaults to `REMOTE_COMMAND` (`ssh`).

**REMOTE_RECV**
:   The remote shell command used for `zfs recv`. Defaults to `REMOTE_DEFAULT` (`ssh -n`).

# GENERAL OPTIONS

**DEPTH**
:   Limit the recursion depth of operations to the number of levels indicated. For example, a depth of 1 will only include the indicated _source_ dataset. Has no effect with **REPLICATE** enabled.

**EXCLUDE**
:    Exclude datasets or source snapshots matching the specified exclusion pattern. See _EXCLUSION PATTERNS_ below.

# ZELTA MATCH OPTIONS

**SCRIPTING_MODE**
:   Suppress column headers and separate columns with a single tab. Useful for parsing output in scripts.

**PARSABLE**
:   Output sizes in exact numbers instead of human-readable values like `1M`.

**PROPLIST**
:   Specify a list of `zelta match` columns. See **zelta-match(8)** for more detail.

**LIST_WRITTEN**
:   Calculate data sizes for datasets and snapshots in the summary. Enabled by default.

**CHECK_TIME**
:   Calculate the time of each `zfs list` operation.

# ZFS SEND/RECEIVE OPTIONS

**SEND_OVERRIDE**
:   Override all `zfs send` options with those indicated. For precise and flexible configuration for different circumstances, use the `SEND_*` variables below instead.

**SEND_DEFAULT**
:   Options used for unencrypted filesystems and volumes. Defaults to `-Lce`.

**SEND_RAW**
:   Options used for encrypted datasets. Defaults to `-Lw`.

**SEND_NEW**
:   Additional option used for new datasets. Defaults to `-p`.

**SEND_INTR**
:   Toggle option to transmit intermediate snapshots (`1`, the default) or incremental (`0`).

**SEND_REPLICATE**
:   Options to use in `zelta backup -R` mode. Defaults to `zfs send -LsRw`.

**SEND_CHECK**
:   Attempt to drop unsupported `zfs send` options using a no-op test prior to replication. This feature is not fully implemented.

**RECV_OVERRIDE**
:   Override all `zfs receive` options with those indicated. For precise and flexible configuration, use the `RECV_*` variables instead.

**RECV_DEFAULT**
:   Default `zfs recv` options. Defaults to none.

**RECV_TOP**
:   Additional options for the top dataset during new (full) backup. Defaults to `-o readonly=on`.

**RECV_FS**
:   Additional options for filesystems during a new (full) backup. Defaults to `-u -x mountpoint -o canmount=noauto`.

**RECV_VOL**
:   Additional options for volumes new (full). Defaults to none.

**RECV_PROPS_ADD**
:   Add the list of 'zfs recv -o' properties in the form **property=value**. See 'zfs-receive(8)'.

**RECV_PROPS_DEL**
:   Add the list of 'zfs recv -x' excluded properties. See 'zfs-receive(8)'.

**RECV_PARTIAL**
:   Additional options if RESUME is enabled. Defaults to `-s`.

**RECV_PREFIX**
:   Pipe output through the indicated command, such as `dd status=progress`.

**RESUME**
:   Enable (`1`, the default) or disable automatic resume of interrupted syncs.

# REPLICATION OPTIONS

**SNAP_NAME**
:   Specify a snapshot name. Use the form `$(my_snapshot_program)` to use a dynamically generated snapshot. The default is `$(date -u +zelta_%Y-%m-%d_%H.%M.%S)`.

**SNAP_MODE**
:   Specify when to snapshot during a `zelta backup` operation. Options: `0` (never), `IF_NEEDED` (default, only if source has new data), or `ALWAYS`.

**SYNC_DIRECTION**
:   If both endpoints are remote, use `PULL` (the default) or `PUSH` sync. If set to `0`, traffic will stream through the local host. Note that this feature PUSH and PULL features require appropriate ssh configurations with keys properly installed and/or ssh agent forwarding enabled.

# CLONE OPTIONS

**CLONE_DEFAULT**
:   Options for recursive cloning. Defaults to `-po readonly=off`.

# POLICY OPTIONS
The following options only effect `zelta policy` operations.

**RETRY**
:   Retry failed syncs the indicated number of times.

**JOBS**
:   Run the indicated number of policy jobs concurrently, one for each Site in the configuration.

**BACKUP_ROOT**
:   The relative target path for backup jobs. For example, `bkhost:tank/Backups` would place backups below that dataset (if not overridden by policy).

**ARCHIVE_ROOT**
:   NOT YET IMPLEMENTED. The relative target path used for rotated clones.

**ADD_HOST_PREFIX**
:   Include the source hostname as a parent of the synced target.
- Example: Source `web1:sink/dataset` with `BACKUP_ROOT: tank/backups` becomes `tank/backups/web1/dataset`.

**ADD_DATASET_PREFIX**
:   Similar to `zfs recv -d`, include the indicated number of parent dataset labels for the `BACKUP_ROOT`'s (or specified target's) name. If set to `-1` all labels up to the pool name will be attached to the target name.
- Example: Source `web1:sink/source/dataset` with `BACKUP_ROOT: tank/backups`:
  - `0`: `tank/backups/dataset`
  - `1`: `tank/backups/source/dataset`
  - `-1`: `tank/backups/sink/source/dataset`
- **ADD_HOST_PREFIX** stacks with **ADD_DATASET_PREFIX**. With both enabled, the hostname is prepended first: `tank/backups/web1/source/dataset`.

# EXCLUSION PATTERNS

The EXCLUDE option, or the arguments **\--exclude** or **-X**, contain a comma separated list of patterns to exclude datasets or source snapshots from operations. Excluding a dataset will also exclude its children.

## Pattern Types

**Absolute Dataset Path**
:   Similar to **zfs send \--exclude**, exclude the named source dataset from operations.

    Example: `tank/vm/swap` excludes that specific dataset.

**Relative Dataset Path**
:   Prefix with `/` to exclude the dataset suffix relative to the given dataset name.

    Example: Given the dataset `sink/swap` and the pattern `/swap`: `sink/swap` will be excluded, but `sink/vm/swap` will **not** be excluded.

**Relative Dataset Pattern**
:   Use glob-like matching of `*` (zero or more characters) or `?` (single character). The pattern must start with '/' or '*' and must contain a '/'.

    Examples, given the given _source_ of `sink/data`:

    - `*/swap` would exclude `sink/data/one/swap`, `sink/data/two/swap`, and `sink/data/swap`
    - `/*/swap` would exclude `sink/data/one/swap` and `sink/data/two/swap` but **not** `sink/data/swap`
    - `/vm-*` would exclude `sink/data/vm-one` and its descendants, but **not** `sink/data/vm/one`
    - `/test?` would exclude `sink/data/test1` but **not** `sink/data/test15`

**Snapshot Name**
:   Match snapshots by name. Prefix with `@` to indicate a snapshot.

    Example: `@manual-backup` excludes any snapshot named `manual-backup`.

**Snapshot Pattern**
:   Use glob-like matching of `*` (zero or more characters) or `?` (single character). Snapshot names must begin with `@`.

    Examples:

    - `@*_hourly` excludes snapshots ending in `_hourly`
    - `@snap-2024*` excludes snapshots beginning with `snap-2024`
    - `@auto-*00??` excludes snapshots beginning with `auto-` and ending with 00 and two of any character

### Exclusions Quick Reference

| Pattern Type | Example | Matches |
|--------------|---------|---------|
| Absolute dataset | `tank/vm/swap` | Exact dataset |
| Relative dataset | `/tmp` | Top dataset ending in `/tmp` |
| Relative dataset wildcard | `*/swap` | Any dataset ending in `/swap` |
| Snapshot name | `@manual-backup` | Exact snapshot name |
| Snapshot wildcard | `@*_hourly` | Snapshots matching pattern |

## Behavior Notes

**Datasets**

Excluding a dataset will also exclude its descendants.

**Snapshots**

For incremental replication, at least one common snapshot must remain between source and target. Therefore, snapshot exclusion logic is only meaningful when applied to incremental source snapshots in incremental mode (**SEND_INTR=0** or **-i**). For example, snapshot exclusion is useful for skipping hourly snapshots and but updating dailies.

- Excluding a target's most recent snapshot will cause an incremental to fail
- In intermediate mode (the default), intermediate snapshots will still be included
- Bookmark exclusions are not supported as they serve only as replication sources

# EXAMPLES
Set options via environment for a one-off run:

    export ZELTA_LOG_LEVEL=4
    export ZELTA_REMOTE_COMMAND="ssh -p 2202"
    zelta backup pool/dataset remote:pool/backup

Configure `zelta.conf`:

    # In zelta.conf, variables act as policy scopes but
    # use the same option names.
    LOG_MODE: json
    JOBS: 2

    NYC1:
      BACKUP_ROOT: backuphost:tank/Backups
      RETRY: 3

# SEE ALSO
zelta(8), zelta-backup(8), zelta-clone(8), zelta-match(8), zelta-policy(8), zelta-rotate(8), cron(8), ssh(1), zfs(8)

# AUTHORS
Daniel J. Bell <_bellhyve@zelta.space_>

# WWW
https://zelta.space
