# Retention Strategies

Regular replication accumulates large numbers of snapshots over time, which usually need to be trimmed for space, organization, and compliance. Zelta provides tools to help you decide which snapshots should remain and safely remove candidates.

- `zelta prune` plans and reports candidates. It does not destroy snapshots.
- `zprune` uses the same strategy, previews the exact destroy commands, and destroys after confirmation.

These tools can help you build retention policies based on the snapshot size, age, name, or a variety of patterns. For all available options and examples, see [zelta-prune(8)](/docs/man/zelta-prune/) and [zprune(8)](/docs/man/zprune/).

Note that different host and dataset types need different retention policies and workflows. Replication keeps a dataset tree current; retention decides how much history that particular system should keep. Those decisions may happen on different schedules and should not be treated as one step in the replication job. A compute server may need compact, frequent history, while a vault may retain older snapshots for recovery or compliance.

## Policy Patterns at a Glance

These are starting points, not mandatory policies. Choose the retention shape for
the role of the system, its available space, and the recovery history it must
retain. The command details and safeguards follow below and in the man pages.

| System role | Replication pattern | Retention pattern |
|-------------|--------------------|-------------------|
| Compute host | Frequent backup to a twin or vault | Frequent quantity- or age-based pruning |
| Compute twin | Mirrored backup policy with one side active at a time | Mirrored retention policy, applied to the active history on each side |
| Local backup | Frequent snapshots for fast local recovery | Short, dense history with periodic size-based cleanup |
| Offsite vault | Selective daily or other workload-specific history | Long-term thinning with an aging grid and conservative replica guards |

## The Safe Workflow

Start with a visual plan:

```sh
zelta prune --visual --match-endpoint=vault:vat/Backups/vm compute:cask/vm
```

Then preview the exact `zfs destroy` commands without changing anything:

```sh
zprune --dryrun --match-endpoint=vault:vat/Backups/vm compute:cask/vm
```

When both reviews look correct, run the prompted operation:

```sh
zprune --match-endpoint=vault:vat/Backups/vm compute:cask/vm
```

Without a match endpoint, explicitly disable the guard for both planning and destruction:

```sh
zelta prune --no-prune-guard --visual compute:cask/vm
zprune --no-prune-guard --dryrun compute:cask/vm
zprune --no-prune-guard compute:cask/vm
```

`zprune` requires either `--match-endpoint` or `--no-prune-guard`. Only the acted-on endpoint is destroyed; the match endpoint is never destroyed. Keep `--force` for controlled automation; it bypasses the confirmation prompt.

## Default Retention

When no retention option is supplied, Zelta uses:

```text
--prune-num=30 --prune-time=30days
```

When a match endpoint is supplied, `--prune-guard=latest` is also the default. It protects the latest common snapshot and everything newer on the acted-on endpoint. Among older snapshots, the count and time windows overlap: a snapshot is kept when either rule protects it.

Supplying any retention option replaces the count and time defaults. It does not add another rule to the default pair.

## Count, Age, and Space

Keep the newest 30 eligible snapshots:

```sh
zelta prune --prune-num=30 --match-endpoint=vault:vat/Backups/vm \
    compute:cask/vm
```

Keep snapshots newer than 30 days:

```sh
zelta prune --prune-time=30days --match-endpoint=vault:vat/Backups/vm \
    compute:cask/vm
```

Select the oldest eligible snapshots until the estimated reclaim reaches 150 GiB:

```sh
zelta prune --prune-size=150G --depth=1 \
    --match-endpoint=vault:vat/Backups/vm compute:cask/vm
```

`--prune-size` is evaluated separately for each dataset. Use `--depth=1` or another filter when the reclaim target is meant for one dataset. The `zprune --dryrun` preview gives a more useful reclaim estimate.

Use unambiguous duration units such as `s`, `min`, `h`, `d`, `w`, `mo`, and `y`. Bare numbers mean seconds; `m` is rejected because it is ambiguous.

## Aging Grids

Use `--prune-grid` for a GFS-style lifecycle policy:

```sh
zelta prune --visual --prune-grid='30x1day, 52x1week, 1year' \
    --match-endpoint=vault:vat/Backups/vm compute:cask/vm
```

This keeps one snapshot per day for 30 days, one per week for 52 weeks, and one per year for the remaining history. Grid terms are evaluated left to right. A term without a count applies to all remaining history.

## Scope Filters

Filters narrow the set before retention rules are applied:

```sh
# Only the specified dataset, not its children
zelta prune --depth=1 compute:cask/vm

# Only snapshots whose names begin with zelta
zelta prune --include='@zelta*' compute:cask/vm

# Exclude a dataset subtree
zelta prune --exclude='/temporary' compute:cask/vm
```

Quote patterns so the shell does not expand them. Snapshot patterns begin with `@`; dataset-relative patterns begin with `/`. Filtered-out snapshots remain protected and cannot become candidates.

## Match Endpoint and Replica Guards

Use `--match-endpoint` to protect history that has not safely reached a peer:

```sh
zelta prune --prune-guard=unsynced \
    --match-endpoint=vault:vat/Backups/vm compute:cask/vm
```

- `latest` protects the latest common snapshot and everything newer. It is the default when a match endpoint is supplied.
- `unsynced` protects snapshots unless the match endpoint has the matching snapshot and name. Use this when missing peer history must never be deleted.
- `none` or `--no-prune-guard` disables match-endpoint confirmation.

Clone origins are always protected. The complete strategy combines scope filters, clone protection, replica guards, and retention rules.

## Permissions and Roles

Keep routine backup and retention permissions separate when possible. A backup user generally needs replication permissions, not `destroy`:

```sh
zfs allow -u backup receive:append,create,mount,readonly tank/backups
```

A retention user can be granted snapshot destruction on the backup tree:

```sh
zfs allow -u retention destroy tank/backups
```

For role design and platform caveats, see [ZFS Allow Delegation](/docs/conf/zfs-allow/). For a remote endpoint, destruction runs on that host through the configured SSH transport.

## Operational Cautions

- Review the visual plan and `zprune --dryrun` before the first scheduled destruction.
- ZFS holds, clone origins, and dependencies can prevent destruction; that is a protection, not a failed retention policy.
- Do not manually pipe candidates to `zfs destroy`; `zprune` validates candidates and previews the exact operation it will perform.
- Test a policy on a representative dataset before scheduling it.

## Related Guides

- [Simple Backups](/docs/guides/backup/)
- [Rollback & Recovery](/docs/guides/recovery/)
- [ZFS Allow Delegation](/docs/conf/zfs-allow/)
