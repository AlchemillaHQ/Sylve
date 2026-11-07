% zelta-prune(8) | System Manager's Manual

# NAME

**zelta prune** - report snapshot prune candidates

# SYNOPSIS

**zelta prune** [_OPTIONS_] _endpoint_

# DESCRIPTION

**zelta prune** reports snapshots on an endpoint dataset tree that are candidates for pruning. It does not destroy snapshots. Use **zprune(8)** with the same options after reviewing a strategy.

Only _endpoint_ is evaluated for prune candidates. An optional match endpoint is never destroyed; it is used only for match validation and **--prune-guard** protection.

A pruning strategy combines scope filters, snapshot protections, and retention options. Scope filters first narrow the dataset tree or snapshot names. Clone and replication checks protect snapshots within that scope. Retention options protect snapshots by count, age, or retention grid. `--prune-size` can further limit candidates to an estimated reclaim target.

All selected rules stack. A snapshot is a prune candidate only if it remains in scope and no protection or retention rule keeps it. For example, the default strategy is:

- `--prune-guard=latest`: Use the latest common snapshot between _endpoint_ and the match endpoint, when one exists, as a protected boundary.
- `--prune-num=30`: Keep the newest 30 snapshots older than that protected boundary.
- `--prune-time=30days`: Keep snapshots from the last 30 days.

The count and time windows overlap. Older than a protected replication boundary, the default keeps the newest 30 snapshots or all snapshots from the last 30 days when that protects more. The boundary and everything newer remain protected separately. Specifying any retention option replaces the count and time defaults.

# OPTIONS

## Endpoint

_endpoint_
: Dataset tree containing snapshots to evaluate. **zprune** destroys candidates on this endpoint only.

## Match Endpoint

`--match-endpoint` _guard_, `--guard-endpoint` _guard_, `--prune-guard-endpoint` _guard_
: Second dataset tree used only for match validation and `--prune-guard` protection (and as the grid/latest boundary peer). Snapshots on _guard_ are never destroyed.

  A second positional operand is accepted as the same match endpoint (`zelta prune` _endpoint_ _guard_) but is not shown in the synopsis; prefer the flag. Do not combine `--match-endpoint` with a second positional operand.

Without a match endpoint, **zelta prune** applies no prune guard. **zprune** requires either `--match-endpoint` or `--no-prune-guard`.

## Output Options

General output, logging, and help options work as described in **zelta-options(7)**. The following options are especially relevant to pruning.

`--dryrun`, `-n`
: Display the **zfs list** command(s) that would be used.

`--no-ranges`
: Disable range compression. By default, consecutive snapshots are emitted as ZFS snapshot ranges suitable for **zfs destroy(8)**.

`--verbose`, `-v`
: Increase verbosity. Specify once for operational detail, twice for debug output.

`--visual`
: Replace the candidate list with `🔹` for each protected snapshot and `❌` for each prune candidate. Snapshots appear in creation order, oldest first. Terminals may wrap long rows. Visual output is written directly to standard output rather than through the normal logging modes.

## Filters

Scope filters are processed before retention options. For example, `--depth=1` considers only the top-level dataset and ignores its children. Snapshots excluded by a scope filter cannot become prune candidates. Snapshots that are clone origins are always protected.

The following global filters work as they do for other Zelta verbs; see **zelta-options(7)**.

`--depth`, `-d` _LEVELS_
: Limit dataset-tree recursion depth. A depth of `1` includes only the specified dataset.

`--exclude`, `-X` _PATTERN_
: Exclude datasets or snapshots matching _PATTERN_. See _INCLUDE AND EXCLUDE PATTERNS_ in **zelta-options(7)**.

`--include` _PATTERN_
: Include only datasets or snapshots matching _PATTERN_. See _INCLUDE AND EXCLUDE PATTERNS_ in **zelta-options(7)**.

`--prune-name` _PATTERN_
: Match snapshot names with a glob (`*`, `?`), without a leading `@`. Built-ins are `zfsnap` (`zfsnap_*`), `truenas` (`auto-*`), and `sanoid` (`autosnap_*`).

Additionally, **zelta prune** protects candidates based on their replication state when a match endpoint is supplied.

`--prune-guard=latest`
: When _endpoint_ and the match endpoint have a common snapshot, protect the latest match and everything newer on _endpoint_. If no match exists, evaluate from the latest snapshot on _endpoint_. This is the default when a match endpoint is given.

`--prune-guard=unsynced`
: Require the candidate snapshot to be present on the match endpoint. Snapshots not confirmed there are protected.

`--prune-guard=none`, `--no-prune-guard`
: Do not require a match endpoint or confirm that candidates exist on it. If a match endpoint is supplied, its latest common snapshot still defines the retention boundary. **zprune** requires this option when a match endpoint is omitted.

## Retention Options

Retention options apply within the scope selected by `--depth`, `--include`, `--exclude`, and `--prune-guard`. Count, time, and grid options overlap: a snapshot is protected when any selected retention option keeps it. If no retention options are given, **zelta prune** uses `--prune-num=30 --prune-time=30days`.

`--prune-grid` _GRID_
: Keep snapshots in one or more aging intervals written as [_COUNT_`x`]_INTERVAL_. For example, `30x1day, 52x1week, 1year` keeps one snapshot per day for 30 days, then one per week for 52 weeks, then one per year for all older history.

Grid terms are evaluated from left to right. A term without _COUNT_ applies to all remaining history. Separate terms with commas or vertical bars. Whitespace is allowed.

Grid ages are measured backward from the latest common snapshot between _endpoint_ and the match endpoint when one exists, or from the latest snapshot or bookmark on _endpoint_ otherwise. **zelta prune** protects the newest snapshot found in each interval.

`--prune-num` _N_
: Keep the newest _N_ snapshots.

`--prune-size` _SIZE_
: Select the oldest remaining snapshots until their estimated reclaim reaches at least _SIZE_. Suffixes such as `M`, `G`, and `T` are supported.

Like all other options, each dataset is evaluated separately. On a dataset tree, every dataset is evaluated against the full _SIZE_. Use `--depth=1` or another filter to limit one dataset. The estimate assumes sequential oldest-first pruning; use the **zprune(8)** preview for a more accurate estimate of reclaimed space.

`--prune-time` _TIME_
: Keep snapshots newer than _TIME_ relative to the current time.

### Duration Syntax

Bare numbers are seconds. Use unambiguous units for anything else:

```text
seconds   s, sec, second, seconds
minutes   mi, min, minute, minutes
hours     h, hour, hours
days      d, day, days
weeks     w, week, weeks
months    mo, mon, month, months
years     y, year, years
```

The unit `m` is ambiguous and is rejected. Use `mi` for minutes or `mo` for months. Months are treated as 30 days and years as 365.25 days.

# OUTPUT FORMAT

By default, **zelta prune** outputs ZFS snapshot names or ranges, one per line:

```text
pool/dataset@oldest_snapshot%newest_snapshot
```

With `--no-ranges`, each candidate snapshot is emitted individually:

```text
pool/dataset@snapshot
```

The output is suitable for review and for **zprune(8)**. Manual piping to **zfs destroy** is discouraged; **zprune** previews candidates with **zfs destroy -nvp** and prompts before deletion.

With `--visual`, each dataset is followed by one symbol per snapshot:

```text
tank/data
❌❌❌❌🔹🔹🔹🔹
```

Snapshots run from oldest to newest. A `🔹` is protected; a `❌` is a prune candidate. The symbols show the result of the complete strategy, including filters, prune guards, and overlapping retention options.

# EXAMPLES

Inspect the default strategy before working with its candidate list:

```sh
zelta prune --visual --match-endpoint=backup:tank/data tank/data
```

When a common snapshot exists, the default protects that latest match and everything newer. Among older snapshots, it also protects the newest 30 and all snapshots from the last 30 days. Diamonds may extend farther into history when the time window protects more than the count window.

Select the oldest snapshots until their estimated reclaim reaches 150 GiB. Limiting depth makes the reclaim apply only to `tank/data`:

```sh
zelta prune --visual --prune-size=150G --depth=1 tank/data
```

Add a match endpoint when old snapshots should be candidates only after reaching the peer:

```sh
zelta prune --visual --prune-size=150G --prune-guard=unsynced \
    --match-endpoint=backup:tank/data tank/data
```

Comparing this with the preceding size-only view reveals old history that the replica guard protects because it is missing from the match endpoint.

Compare count and time windows independently:

```sh
zelta prune --visual --prune-num=30 tank/data
zelta prune --visual --prune-time=30days tank/data
```

Limit the strategy to snapshots whose names begin with `zelta`. Snapshots with other names remain protected:

```sh
zelta prune --visual --include='@zelta*' tank/data
```

Keep one snapshot per week throughout all history considered by the filters:

```sh
zelta prune --visual --prune-grid=1week tank/data
```

Apply a longer data-lifecycle grid:

```sh
zelta prune --visual --prune-grid='30x1day, 52x1week, 1year' \
    tank/data
```

After inspecting a strategy, report its candidates in the default pipeable format:

```sh
zelta prune --prune-grid='30x1day, 52x1week, 1year' tank/data
```

Preview the exact **zfs destroy** commands and confirm destructive pruning. Because this example has no match endpoint, **zprune** requires an explicit `--no-prune-guard`:

```sh
zprune --no-prune-guard \
    --prune-grid='30x1day, 52x1week, 1year' tank/data
```

# EXIT STATUS

Returns 0 on success and non-zero on error.

# NOTES

**zelta prune** is nondestructive: it only plans candidates. Use **zprune(8)** when you intend to destroy snapshots, and review the preview carefully.

This command is driven by the same comparison engine as **zelta match**. See **zelta-match(8)** for endpoint matching behavior.

# SEE ALSO

**zelta(8)**, **zprune(8)**, **zelta-options(7)**, **zelta-match(8)**, **zelta-backup(8)**, **zfs(8)**, **zfs-destroy(8)**

# AUTHORS

Daniel J. Bell <_bellhyve@zelta.space_>

# WWW

https://zelta.space
