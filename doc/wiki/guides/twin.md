# Zelta Twin

Zelta Twin is an asynchronous cluster pattern built from ordinary Zelta commands. Two ZFS dataset trees act as replication partners. One side is active and writable. The other side is a read-only, forkable, cloneable, accessible twin.

People may also search for this as standby nodes, replicas, mirrors, secondary instances, failover partners, or asynchronous clustering. In Zelta docs, the preferred term is **Zelta Twin**.

## Why Twin Is Different from a Cold Backup

A normal backup is optimized for history and recovery. A twin is optimized for continuity.

The standby twin is still a real ZFS dataset tree. You can inspect it, clone it, test it, back it up again, or promote it during a failover. It is not a sealed archive or an opaque backup file.

Zelta Twin works because Zelta operations compose cleanly:

- `zelta policy` runs the recurring reciprocal backup jobs.
- `zelta backup` keeps the standby side current.
- `zelta lock` makes the active side read-only during planned promotion.
- `zelta propsync` copies local ZFS properties needed by the promoted side.
- `zelta unlock` makes the promoted side writable.
- `zelta failover` composes the guarded promotion workflow.

## Basic Model

```text
normal operation:

compute1:tank/services   active, writable
        |
        | zelta backup
        v
compute2:tank/services   standby, readonly

after failover:

compute1:tank/services   standby, readonly
        ^
        | zelta backup
        |
compute2:tank/services   active, writable
```

Only one side should be writable at a time. Zelta can help lock, sync, and unlock, but it cannot make split-brain safe if operators or external automation mount both sides read-write.

## Minimal Policy

This policy runs from a bastion or one of the twin management accounts. It defines both directions. `SNAP_MODE: IF_NEEDED` lets the active side produce snapshots when it has new writes, while the read-only standby side normally has nothing new to snapshot.

```yaml
SNAP_TIME: 30min
SNAP_MODE: IF_NEEDED
SEND_INTR: 0
JOBS: 2

Twin:
  compute1.example.com:
    options:
      BACKUP_ROOT: compute2.example.com:tank
    datasets:
      - tank/vm
      - tank/jail

  compute2.example.com:
    options:
      BACKUP_ROOT: compute1.example.com:tank
    datasets:
      - tank/vm
      - tank/jail
```

Run it from cron:

```cron
*/15 * * * * zelta policy Twin
```

The same pattern scales to more dataset groups. Keep the policy readable: twin configs should make it obvious which tree pairs with which tree.

## Delegation

On both twin roots, delegate send and append-only receive permissions to the twin user:

```sh
zfs allow -u twin send:raw,receive:append,snapshot,hold,bookmark,create,mount,readonly,clone,rename,volmode,compression,recordsize tank
```

Use `receive:append` rather than plain `receive` for the routine twin user when your OpenZFS version supports it. A twin user needs `clone` and `rename` for flexible recovery and rotation workflows, but that does not mean it should also be able to rewrite history with destructive receives.

For encrypted datasets, prefer `send:raw` so the twin user can replicate encrypted data without permission to send decrypted streams.

See [ZFS Allow Delegation](/docs/conf/zfs-allow/) for the role model and legacy platform notes.

## Normal Operation

During normal operation:

- The active side is writable.
- The standby side is read-only.
- The policy includes both directions.
- The active-to-standby direction sends new snapshots.
- The standby-to-active direction normally has nothing to send.

Verify state with:

```sh
zelta match compute1.example.com:tank/vm compute2.example.com:tank/vm
zelta match compute1.example.com:tank/jail compute2.example.com:tank/jail
```

Use dry-run output before changing a policy:

```sh
zelta policy -n Twin
```

## Planned Failover

For a planned promotion, use `zelta failover`:

```sh
zelta failover compute1.example.com:tank/vm compute2.example.com:tank/vm
zelta failover compute1.example.com:tank/jail compute2.example.com:tank/jail
```

The composed workflow is:

```sh
zelta lock compute1.example.com:tank/vm
zelta backup compute1.example.com:tank/vm compute2.example.com:tank/vm
zelta propsync compute1.example.com:tank/vm compute2.example.com:tank/vm
zelta unlock compute2.example.com:tank/vm
```

After promotion, the reciprocal policy keeps the old primary current as the new standby.

## Failback

Failback is the same pattern in reverse:

```sh
zelta failover compute2.example.com:tank/vm compute1.example.com:tank/vm
zelta failover compute2.example.com:tank/jail compute1.example.com:tank/jail
```

Always verify with `zelta match` before and after a failover or failback.

## Production Checklist

- Use a dedicated `twin` SSH account.
- Use SSH keys or agent forwarding from a bastion; do not distribute root credentials.
- Delegate `receive:append`, not plain `receive`, for routine twin replication where supported.
- Delegate `send:raw` for encrypted dataset trees.
- Keep only one side writable.
- Schedule `zelta policy` often enough for your recovery point objective.
- Use `zelta policy -n` after policy edits.
- Use `zelta match` before promotion.
- Test `zelta failover -n` before depending on it.
- Keep retention and destructive pruning in a separate role.

## When Not To Use Twin

Zelta Twin is asynchronous. It is not a synchronous storage protocol, lock manager, quorum system, or automatic split-brain resolver.

Use Twin when you want a simple, inspectable, forkable standby dataset tree. Use a different design when the application requires synchronous writes across nodes or automatic multi-writer clustering.

## See Also

- [ZFS Allow Delegation](/docs/conf/zfs-allow/)
- [Policy-Based Automatic Backups](/docs/guides/policy/)
- [Failover Workflows](/docs/guides/sync/)
- [Manual: zelta failover](/docs/man/zelta-failover/)
