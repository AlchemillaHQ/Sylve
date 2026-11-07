# Failover Workflows

For active-passive systems, keep the standby dataset tree read-only and verify it before promotion.

When both directions are defined as recurring policy jobs, this pattern is a [Zelta Twin](/docs/guides/twin/): an asynchronous cluster pair where either side can become the active dataset tree. Twin is the full operator guide; this page is the short promotion path.

## Guarded Promotion

The high-level workflow in Zelta 1.2 is `zelta failover`:

```sh
zelta failover primary.example.com:tank/service standby.example.com:tank/service
```

`zelta failover` composes the safety steps: lock the active source, perform a final backup, sync local ZFS properties, and unlock the promoted target. See [zelta-failover(8)](/docs/man/zelta-failover/).

## Manual Steps

Use lower-level commands when you need to script or pause between steps:

```sh
zelta match primary.example.com:tank/service standby.example.com:tank/service
zelta lock primary.example.com:tank/service
zelta backup primary.example.com:tank/service standby.example.com:tank/service
zelta propsync primary.example.com:tank/service standby.example.com:tank/service
zelta unlock standby.example.com:tank/service
zelta match primary.example.com:tank/service standby.example.com:tank/service
```

| Command | Role |
|---------|------|
| `zelta lock` | Make the active tree safe to leave (readonly / unmount order) |
| `zelta backup` | Final incremental to the standby |
| `zelta propsync` | Copy local properties the promoted side needs |
| `zelta unlock` | Make the promoted side writable |

See [zelta-failover(8)](/docs/man/zelta-failover/) for the lower-level command details.

## Rules of thumb

- Do not run both sides read-write at the same time.
- Always verify with `zelta match` before and after promotion.
- If `zelta match` reports divergence (not merely behind), fix backup continuity with [zelta rotate](/docs/guides/recovery/) before expecting a normal backup or failover.
- After promotion, reverse your recurring backup direction (or rely on twin policy that already defines both sides).

## Related

- [Zelta Twin](/docs/guides/twin/) — reciprocal policy, allow recipes, day-2 operations
- [Rollback & Recovery](/docs/guides/recovery/) — clone, revert, rotate
- [Policy-Based Automatic Backups](/docs/guides/policy/)
