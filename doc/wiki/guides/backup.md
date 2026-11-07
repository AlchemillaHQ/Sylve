# Simple Backups

Compare first, back up second, then verify with the same comparison command.

```sh
zelta match tank/data backup@storage.example.com:tank/Backups/data
zelta backup tank/data backup@storage.example.com:tank/Backups/data
zelta match tank/data backup@storage.example.com:tank/Backups/data
```

`zelta backup` creates snapshots when needed, detects the best incremental stream, and creates read-only backup datasets by default. Run the same command again later to update incrementally. If nothing changed on the source, Zelta will not create a pointless new snapshot.

For flags and full option lists, see [zelta-backup(8)](/docs/man/zelta-backup/) and [zelta-options(7)](/docs/man/zelta-options/).

## Defaults Worth Knowing

Most backups need only the two endpoints and no switches:

| Behavior | Default |
|----------|---------|
| Recursive dataset tree | on |
| Resume tokens | on |
| Raw / encrypted-friendly send | on when appropriate |
| Intermediate snapshots | sent (history preserved) |
| Target `readonly=on` | on |
| Target mountpoints | inherited / `canmount=noauto` on new filesystems |

These target defaults avoid accidental overlays and keep replicas safe to receive again. Details live in the man page; the short version is: backups should not surprise you at mount time.

## Narrow What You Send

Use `--include` and `--exclude` to narrow dataset or snapshot selection. Snapshot patterns begin with `@`.

```sh
# Skip hourly snapshots on the target stream
zelta backup --exclude '@hourly_*' tank/data backup:tank/Backups/data

# Only a critical child and daily snapshots
zelta backup --include '/critical,@daily_*' tank/data backup:tank/Backups/data

# Name new snapshots on the source
zelta backup --snap-prefix=daily --include='@daily*' \
  alpha:ark/ds vault:vat/ds
```

Global depth, include, and exclude filters work across Zelta commands. When a ZFS send flag is set explicitly, it overrides Zelta defaults for that send.

## Optional Send Bookmarks

`--bookmark` creates a ZFS bookmark for the latest successfully sent snapshot, tagged with useful host context. It is off by default.

```sh
zelta backup --bookmark tank/data backup:tank/Backups/data
```

Bookmarks help incremental continuity and point-of-use telemetry. The backup user needs the ZFS `bookmark` permission; see [ZFS Allow Delegation](/docs/conf/zfs-allow/).

## Intermediate Snapshots

By default, intermediate snapshots between the match and the latest source snapshot are included so the target keeps a usable history. That costs transfer size and target space.

If you only need the latest common chain for a fast update, filter with snapshot patterns or use intermediate-skip settings documented in [zelta-options(7)](/docs/man/zelta-options/) (`SEND_INTR` / related flags). Prefer filters when you still want named history on the target (for example only `@daily*`).

## Schedule Or Policy

For recurring backups, either schedule direct commands with cron:

```cron
0 */6 * * * zelta backup tank/data backup@storage:tank/Backups/data
```

or move repeated jobs into [Policy-Based Automatic Backups](/docs/guides/policy/).

For active-passive pairs, see [Zelta Twin](/docs/guides/twin/) and [Failover Workflows](/docs/guides/sync/).
