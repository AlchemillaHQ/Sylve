# Guides

These guides cover common Zelta workflows. Start with direct `zelta backup` and `zelta match` commands, then move repeated work into `zelta policy`.

Flags and precise behavior live in the [manual pages](/docs/man/) (also published on this site). Guides answer *when* and *how* to compose commands.

| Guide | Use when |
|-------|----------|
| [Simple Backups](/docs/guides/backup/) | Match → backup → verify; filters; bookmarks |
| [Policy-Based Automatic Backups](/docs/guides/policy/) | Many hosts/jobs, `import:` fragments, bastions |
| [Zelta Twin](/docs/guides/twin/) | Reciprocal pair, day-2 twin operations |
| [Failover Workflows](/docs/guides/sync/) | Promote standby; lock / propsync / unlock |
| [Rollback & Recovery](/docs/guides/recovery/) | Clone, revert, rotate, prune decision tree |
| [Retention Strategies](/docs/guides/retention/) | Snapshot retention, replica guards, and safe destruction |
| [JSON Output](/docs/guides/json/) | Logs and monitoring |

Not sure which command? See [Which Command?](/docs/overview/#which-command) in Core Concepts.

For installation and delegation, see [Installation & Configuration](/docs/install/), [SSH Configuration](/docs/conf/ssh/), and [ZFS Allow Delegation](/docs/conf/zfs-allow/).
