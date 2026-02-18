% zelta-backup(8) | System Manager's Manual

# NAME

**zelta backup** - replicate a ZFS dataset tree

# SYNOPSIS

**zelta backup** [_OPTIONS_] _source_ _target_

# DESCRIPTION

**zelta backup** recursively replicates snapshots from a _source_ ZFS dataset to a _target_ dataset. Both _source_ and _target_ may be local or remote via **ssh(1)**.

As with other Zelta commands, **zelta backup** works recursively on a dataset tree. The _target_ dataset must be a replica of the _source_ or must not exist.

Prior to replication, **zelta backup** analyzes both source and target to automatically select optimal `zfs send` and `zfs recv` options. This process includes property inspection, parent dataset creation if needed, snapshot GUID comparison via **zelta match**, and one or more send/recv operations to update the target.

## Replication Process

**zelta backup** performs these operations:

1. **Property Analysis**: Source and target properties are checked using `zfs get` to detect encryption, written state, and other features.
2. **Parent Creation**: If the target dataset's parent does not exist, it will be created with `zfs create`.
3. **Snapshot Comparison**: Using **zelta match**, a snapshot GUID comparison is performed with `zfs list` to identify matching snapshots and determine the optimal replication strategy.
4. **Snapshot Creation**: If the source has uncommitted changes and no recent snapshot exists, a new snapshot is created (unless `--no-snapshot` is specified).
5. **Incremental Sync**: One or more `zfs send/zfs recv` operations are performed to update the target, using incremental sends when possible.

## Send Options

The following `zfs send` options are applied based on dataset properties:

- **Default**: `--large-block`, `--compressed`, `--embed`
- **Encrypted Datasets**: `--large-block`, `--raw`
- **New/Full Syncs**: Also includes `--props`
- **Replicate Mode** (`-R`): `--replicate`, `--large-block`, `--raw`, `--skip-missing`

## Target Safety Features

To ensure safe and repeatable replication, the following measures are applied to the target:

- The property `readonly=on` is set on the topmost dataset requested
- Synced filesystems are not mounted on the _target_ during replication
- On newly backed up filesystems, property `canmount=noauto` is set
- On newly backed up filesystems, mountpoints are inherited to prevent overlapping mounts

## Source and Target Endpoints

Remote dataset endpoint names follow **scp(1)** conventions and require standard ZFS utilities and SSH access. Note that Zelta does **not** need to be installed on remote ZFS servers.

Examples:

    Local:  pool/dataset
    Local:  pool/dataset@snapshot
    Remote: user@example.com:pool/dataset
    Remote: user@example.com:pool/dataset@snapshot

# OPTIONS

## Endpoint Arguments (Required)

If both endpoints are remote, the default behavior is **pull replication** (`--pull`). This requires that the _target_ user have ssh access to the _source_, typically provided by ssh keys or agent forwarding. For advanced ssh configuration, see _https://zelta.space_.

_source_
: The dataset to replicate. If a snapshot is specified, replication will sync up to that snapshot.

_target_
: The dataset which will be updated.

**Output Options**

**-v, \--verbose**
: Increase verbosity. Specify once for operational detail, twice (`-vv`) for debug output.

**-q, \--quiet**
: Quiet output. Specify once to suppress warnings, twice (`-qq`) to suppress errors.

**-j, \--json**
: Output results in JSON format. See **zelta-options(7)** for details.

**-n, \--dryrun, \--dry-run**
: Display `zfs` commands without executing them.

## Connection Options

**\--push, \--pull, \--sync-direction** _DIRECTION_
: When both endpoints are remote, use `PULL` (default) or `PUSH` sync direction.

**\--recv-pipe** _COMMAND_
: Pipe `zfs receive` output through the indicated command, such as `dd status=progress`.

## Dataset Options

**-d, \--depth** _LEVELS_
: Limit recursion depth. For example, a depth of 1 includes only the specified dataset.

**\--exclude, -X** _PATTERN_
: Exclude /dataset/suffix, @snapshot, or #bookmark beginning with the indicated symbol. Wildcards `?` and `*` are permitted. See **zelta-match(8)**.

## Snapshot Options

**\--no-snapshot**
: Do not create snapshots. If a snapshot is needed for replication, the operation will fail.

**\--snapshot, \--snapshot-always**
: Force snapshot creation even if the source has no uncommitted changes.

**\--snap-name** _NAME_
: Specify snapshot name. Use `$(command)` for dynamic generation. Default: `$(date -u +zelta_%Y-%m-%d_%H.%M.%S)`.

**\--snap-mode** _MODE_
: Specify when to snapshot: `NEVER` (or `0`), `IF_NEEDED` (default, only if source has new data or no recent snapshot), or `ALWAYS`.

## Sync Options

**-R, \--replicate**
: Use `zfs send --replicate` instead of Zelta's per-snapshot analysis. This sends all snapshots, bookmarks, and properties in a single process but provides less granular control over send options.

**-I**
: Sync all intermediate source snapshots using `zfs send -I` for updates. This is the default behavior. See **-i**.

**-i, \--incremental**
: Sync only the latest snapshot, skipping any intermediate snapshots. For full backups only the latest snapshot will be sent. For incremental backups, `zfs send -i` will be used.

**\--resume, \--no-resume**
: Enable (default) or disable automatic resume of interrupted syncs.

## Advanced Override Options

**WARNING:** These options override Zelta's automatic safety and efficiency logic. Incorrect usage can result in target data loss or decrypted backup streams. Use only when you understand the implications.

Zelta automatically applies non-destructive and efficient `zfs send` and `zfs recv` options based on dataset type and context. These defaults can be modified three ways:

1. **Granular Override Options** — Override specific contexts (encrypted vs unencrypted, filesystem vs volume, etc.)
2. **Dataset Tree Override Options** — Replace all `zfs send` or `zfs recv` options for an entire backup job
3. **Pass-Through Override Flags** — Directly pass unambiguous `zfs send` or `zfs recv` flags

**Most Common Use Case: Recompress Backups**

Typically, users adjust Zelta options because they would like to aggressively compress data on their backup endpoints. This is best done with the `--send-default` and `--recv-default` flags, which will not prevent Zelta from sending encrypted backups in raw (encrypted) format:

    zelta backup --send-default -Le --recv-default '-o compression=zstd-5' source backup

All overrides can also be configured globally in `zelta.env` or per-job via `zelta.conf`.

### Important: Options with Special Handling

Several `zfs send` and `zfs recv` options have special meaning in Zelta and should generally **not** be included in override strings:

**Zelta uses these internally:**
- `zfs send --parsable (-P)` — Used for progress tracking
- `zfs recv -v` — Used for operational feedback

**These have Zelta-specific behavior (see OPTIONS above):**
- `-I` and `-i` — Control incremental behavior; use the flags documented above instead
- `--exclude, -X` — Has additional Zelta functionality beyond the `zfs send` version
- `--dryrun, -n` — Shows commands that would run; handled by Zelta
- `-t` — Used automatically for resume tokens

**Not supported:**
- `zfs send -S` — Unsupported
- `zfs recv -A` — Should be used manually when needed

### Granular Override Options

For precise control in a dataset tree with mixed types, override specific contexts. These options are **cumulative**—for example, a filesystem receive will combine options from `--recv-default`, `--recv-top` (if applicable), and `--recv-fs`.

**--send-default** *"OPTIONS"*
: `zfs send` options for **unencrypted** datasets (default: `-Lce`)

**--send-raw** *"OPTIONS"*
: `zfs send` options for **encrypted** datasets (default: `-Lw`)

**--send-new** *"OPTIONS"*
: Additional `zfs send` options during full (non-incremental) backups (default: `-p`)

**--recv-default** *"OPTIONS"*
: `zfs recv` options for all datasets (none by default)

**--recv-top** *"OPTIONS"*
: Additional `zfs recv` options for the topmost dataset only (default: `-o readonly=on`)

**--recv-fs** *"OPTIONS"*
: Additional `zfs recv` options for filesystem datasets (default: `-u -o canmount=noauto -x mountpoint`)

**--recv-vol** *"OPTIONS"*
: Additional `zfs recv` options for volume datasets (default: `-o volmode=none`)

**Examples:**

    # Allow target to recompress unencrypted data
    zelta backup --send-default "-Le" source target

    # Change volume mode on target
    zelta backup --recv-vol "-o volmode=dev" source target

    # Avoid mountpoint permission issues on some systems
    zelta backup --recv-fs "-o mountpoint=none" source target

### Dataset Tree Override Options

These options **replace all context-specific defaults** for an entire backup job. Use when you need complete control over a specific command.

**--send-override** *"OPTIONS"*
: Override all default `zfs send` options

**--recv-override** *"OPTIONS"*
: Override all default `zfs recv` options

**Example:**

    # Use minimal `zfs send` to send uncompressed (**and decrypted!**) streams and recompress aggressively on the target
    zelta backup --send-override "-L" --recv-override "-o compression=zstd-5" source target

### Pass-Through Override Flags

If **any** pass-through flag is specified, it replaces **all** automatic options for that command. For example, specifying `-L` alone means `zfs send` will receive **only** `-L`—no compression, no embedded data, no properties.

The following unambiguous `zfs send` and `zfs recv` flags are passed through directly:

**zfs send:** `-b, --backup, --embed, --holds, -L, --largeblock, --proctitle, --props, --raw, --skipmissing, -V, -w`

**zfs recv:** `-F, -M, -u, -o, -x`

**Ambiguous or unsupported flags:**
- Single-dash options with multiple meanings are **not supported**: `-c, -d, -e, -h, -s`
- Options with Zelta-specific handlers (see above): `-I, -i, -R, -X, -n`

**Example:**

    # Recompress at target with zstd-5
    # WARNING: This disables encrypted sends!
    zelta backup -L -o compression=zstd-5 source target

**When in doubt, use the granular override options instead.**

# EXAMPLES

The same command works for both new and existing target datasets.

Local replication with automatic snapshot creation:

    zelta backup sink/source/dataset tank/target/dataset

Remote to local synchronization:

    zelta backup remote_host:sink/source/dataset tank/target/dataset

Dry run to preview commands:

    zelta backup -n sink/source/dataset tank/target/dataset

Replicate with custom snapshot naming:

    zelta backup \--snap-name "backup_$(date +%Y%m%d)" \
        sink/source/dataset tank/backups/source/dataset

Incremental sync, skipping intermediate snapshots:

    zelta backup -i sink/source tank/target

Limit recursion depth:

    zelta backup -d 2 sink/source tank/target

# EXIT STATUS

Returns 0 on success, non-zero on error.

# NOTES

See **zelta-options(7)** for environment variables, `zelta.env` configuration, and `zelta policy` integration.

The `zelta sync` command is a convenience alias for `zelta backup -i` and may be extended in future versions with additional optimizations for continuous replication workflows.

# SEE ALSO

zelta(8), zelta-options(7), zelta-match(8), zelta-policy(8), zelta-clone(8), zelta-revert(8), zelta-rotate(8), zelta-snapshot(8), ssh(1), zfs(8), zfs-send(8), zfs-receive(8)

# AUTHORS

Daniel J. Bell <_bellhyve@zelta.space_>

# WWW

https://zelta.space
