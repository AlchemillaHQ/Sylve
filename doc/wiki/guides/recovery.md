# Rollback & Recovery

Prefer nondestructive recovery paths first. Zelta keeps the safety boundary visible: commands that start with `zelta` do not destroy data. Snapshot destruction is a separate step with `zprune` after `zelta prune` plans it.

For option details, see [zelta-clone(8)](/docs/man/zelta-clone/), [zelta-revert(8)](/docs/man/zelta-revert/), [zelta-rotate(8)](/docs/man/zelta-rotate/), and [zelta-prune(8)](/docs/man/zelta-prune/).

## Decision Tree

```text
What do you need?

1. One file or directory from an existing snapshot
   → Use .zfs/snapshot (or your OS mount of that snapshot)
   → No Zelta required

2. A writable copy to inspect or fix without touching production
   → zelta clone  (from source or backup)

3. Production should look like an older snapshot, keep broken state
   → zelta revert  (rename + clone; not zfs rollback)

4. Source and backup diverged; you need backups to continue
   → zelta rotate  (preserve both histories, restore continuity)

5. Promote a standby twin to active
   → zelta failover  (see Failover Workflows / Twin)

6. Free space by deleting old snapshots
   → zelta prune (plan) then zprune (destroy)
```

When unsure, start with `zelta match` so you know whether the trees still share a snapshot.

## File-Level Recovery

If the dataset is still available, read from `.zfs/snapshot` first. That is often enough and does not rewrite the live dataset.

## Writable Recovery Environment

Use `zelta clone` when you need a writable tree from a backup or snapshot without changing the original:

```sh
zelta clone backup:tank/Backups/data tank/recovery/data-test
```

Clones are zero-cost references; they share existing storage until they diverge. See [zelta-clone(8)](/docs/man/zelta-clone/).

## Rewind In Place: `zelta revert`

Use `zelta revert` to rewind a dataset to a previous snapshot while keeping the current state under a renamed name. Prefer this over `zfs rollback`, which destroys intermediate history.

```sh
zelta revert tank/data
zelta revert tank/data@yesterday
```

Typical case: a bad update broke a service. Revert restores a known-good tree; the broken tree remains for forensics.

After a local revert, a replica may no longer match. Use `zelta rotate` (below) if backups must continue from the rewound source.

## Restore Backup Continuity: `zelta rotate`

Use `zelta rotate` when source and target have diverged and you want both histories kept:

- The source was cloned or reverted (`zelta revert`)
- The source was rolled back (`zfs rollback`)
- The target was written or otherwise diverged

```sh
zelta rotate tank/data backup:tank/Backups/data
```

Rotate renames the diverged target, clones from a shared match, and incrementally syncs so ordinary `zelta backup` can resume. Then verify:

```sh
zelta match tank/data backup:tank/Backups/data
zelta backup tank/data backup:tank/Backups/data
```

If the top-level target shares no snapshot with the source (or source origin), rename the target yourself and run a full `zelta backup`. Details: [zelta-rotate(8)](/docs/man/zelta-rotate/).

## Snapshot Retention (Separate Axis)

Planning and destruction stay split on purpose:

```sh
zelta prune --prune-time 30d tank/data backup:tank/Backups/data
zprune --prune-time 30d tank/data backup:tank/Backups/data
```

`zelta prune` previews and can pipe candidates. `zprune` is the only tool in the suite meant to destroy snapshots. For complete retention strategies, see [Retention Strategies](/docs/guides/retention/). Keep retention users and permissions separate from routine backup users; see [ZFS Allow Delegation](/docs/conf/zfs-allow/).

## Related

- [Failover Workflows](/docs/guides/sync/) and [Zelta Twin](/docs/guides/twin/) for promotion
- [Simple Backups](/docs/guides/backup/) for the match → backup loop
- Man index: [Manual Pages](/docs/man/)
