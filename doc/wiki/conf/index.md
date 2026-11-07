# Configuration

Zelta configuration is split between environment defaults, policy files, SSH access, and ZFS delegation.

- [Environment & Policy Files](/docs/conf/env/): Which file to edit, syntax differences, option precedence, and references.
- [zelta.env](/docs/conf/zelta-env/): Cross-command defaults (shell `KEY=value` syntax).
- [zelta.conf](/docs/conf/zelta-conf/): Policy jobs for `zelta policy` (YAML-like, `import:` fragments).
- [SSH Configuration](/docs/conf/ssh/): Keys, bastions, agent forwarding, and connection tuning.
- [ZFS Allow Delegation](/docs/conf/zfs-allow/): Non-root ZFS permissions for backup, recovery, failover, and pruning.
- [Getting Started With ZFS](/docs/conf/zfs/): ZFS background for new users.

For reciprocal failover partners, see [Zelta Twin](/docs/guides/twin/).

Use user-local configuration under `~/.config/zelta` when present; otherwise Zelta uses `/usr/local/etc/zelta`.
