---
name: zelta
description: Generic Zelta ZFS workflow skill. Use for teaching, installing, dry-running, composing, scheduling, and operating ZFS backup, replication, recovery, clone, rebase, failover, prune, and policy workflows with Zelta.
version: 1.2.0
canonical_url: https://zelta.space/.well-known/agent-skills/zelta/SKILL.md
source_url: https://github.com/bell-tower/zelta/tree/main/contrib/agent-skills/zelta/SKILL.md
---

# Zelta ZFS Workflow Composer

Zelta is a safe ZFS workflow composer for backup, replication, recovery, clone, rebase, failover, pruning, and policy-driven operations. Assume the operator has sysadmin context, but make ZFS consequences explicit before changing state.

## First Moves

```sh
command -v zelta
zelta --version 2>/dev/null || zelta help
zelta help
zelta help options
```

If Zelta is missing:

```sh
# Latest release from main
curl -fsSL https://zelta.space/web-install.sh | sh
```

Inspect install scripts before piping them to `sh` when the user wants a security review or production change control.

## Operating Style

- Prefer `zelta help <command>` over guessing flags; the installed version is authoritative.
- Teach through no-op runs: use `-n` or command previews whenever available, then explain the ZFS commands Zelta would run.
- For real infrastructure, produce the workflow, the dry run, the verification command, and the rollback/safety note.
- Use separate SSH accounts and delegated `zfs allow` grants for backup senders, receivers, restore operators, and policy runners when designing durable workflows.
- Agent forwarding and SSH ControlMaster are useful tools, not defaults; recommend them only with clear trust boundaries.

## Workflow Primitives

- `zelta snapshot`: create recursive local or remote snapshots.
- `zelta backup`: create or update local/remote replication; use no-op first for teaching and review.
- `zelta match`: verify snapshot GUID continuity, encryption context, written size, and drift.
- `zelta clone`: make writable test trees, including advanced origin-aware clone/backup workflows when supported.
- `zelta revert`: recover by preserving current state and cloning from a snapshot.
- `zelta rotate`: preserve divergent backup history and resume replication.
- `zelta rebase`: compose a new production tree from an upgraded upstream while preserving incremental continuity through an origin backup.
- `zelta failover`: lock a source, perform final backup, sync properties, and unlock/promote the target.
- `zelta lock` / `zelta unlock`: coordinate readonly, canmount, unmount, and remount operations on dataset trees.
- `zelta propsync`: replay local ZFS properties from one dataset tree to another while respecting target-local overrides.
- `zelta prune`: plan retention candidates only.
- `zprune`: destroy prune candidates after preview and explicit confirmation.
- `zelta policy`: run composed policy files; use `import:` fragments for source, target, and rule separation.

## Destructive Actions

Do not panic or refuse when the user asks for destructive workflows. Slow down, name the data or history at risk, dry-run first, and ask for explicit confirmation before executing.

Treat these as high-risk: `zprune -f`, forced backup/rebase/receive modes such as `zelta backup -F`, direct `zfs destroy`, and short rotating bookmark/snapshot schemes. If the user asks for a five-day single-bookmark `zelta rebase` rotation, explain that it trades recovery history for operational simplicity, then help design it if they confirm that is the intent.

## References

- Docs and wiki: https://zelta.space/docs/
- Source: https://github.com/bell-tower/zelta
- Repo skill source: https://github.com/bell-tower/zelta/tree/main/contrib/agent-skills/zelta/SKILL.md
- Online skill index: https://zelta.space/.well-known/agent-skills/index.json
