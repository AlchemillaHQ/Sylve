# Core Concepts

This page clarifies common terms and explains how Zelta works. If you're new to ZFS replication or backup strategies, this is a great place to start.

---

## Basic Usage

The simplest Zelta commands look like this:

**Compare two dataset trees:**
```sh
zelta match source/endpoint target/endpoint
```

**Create or update a backup:**
```sh
zelta backup source/endpoint target/endpoint
```

**Run multiple backup jobs automatically:**
```sh
zelta policy
```

For quick help, use `zelta usage` to see available commands and options, or `zelta help` to view the full manual.

---

## Which Command?

Man pages list every flag. Use this table to pick the workflow, then open the linked guide or man page.

| Goal | Command | Where to read |
|------|---------|----------------|
| See if two trees match | `zelta match` | [First Backup](/docs/start/), [zelta-match(8)](/docs/man/zelta-match/) |
| Create or update a replica | `zelta backup` | [Simple Backups](/docs/guides/backup/), [zelta-backup(8)](/docs/man/zelta-backup/) |
| Many jobs / sites | `zelta policy` | [Policy](/docs/guides/policy/), [zelta-policy(8)](/docs/man/zelta-policy/) |
| Writable test copy | `zelta clone` | [Recovery](/docs/guides/recovery/), [zelta-clone(8)](/docs/man/zelta-clone/) |
| Rewind live dataset, keep old state | `zelta revert` | [Recovery](/docs/guides/recovery/), [zelta-revert(8)](/docs/man/zelta-revert/) |
| Fix diverged source/target | `zelta rotate` | [Recovery](/docs/guides/recovery/), [zelta-rotate(8)](/docs/man/zelta-rotate/) |
| Promote standby twin | `zelta failover` | [Failover](/docs/guides/sync/), [Twin](/docs/guides/twin/) |
| Create a recursive snapshot | `zelta snapshot` | [zelta-snapshot(8)](/docs/man/zelta-snapshot/) |
| Plan snapshot deletion | `zelta prune` | [Recovery](/docs/guides/recovery/), [zelta-prune(8)](/docs/man/zelta-prune/) |
| Destroy planned snapshots | `zprune` | [zprune(8)](/docs/man/zprune/) |

**Safety boundary:** `zelta*` does not destroy data. `zprune` destroys snapshots only.

---

## Endpoint Format

Zelta uses an SCP-like syntax to specify datasets and snapshots:

**Format:** `[username@][hostname:]pool[/dataset][@snapshot]`

**Examples:**
- Local dataset: `tank/data`
- Remote dataset: `server10.biz:tank/files`
- Remote dataset with user: `backup@server11.biz:pool/stuff`
- Remote snapshot: `twin@server12.biz:sink/log@today`

---

## Zelta Terminology

Understanding these terms will help you get the most out of Zelta. For foundational ZFS concepts, see `zfsconcepts(7)` by running `zfs help concepts` to learn about snapshots, bookmarks, and the ZFS filesystem hierarchy.

### Replication & Backup Terms

- **Archive:** A static replica that doesn't need incremental updates, such as backups of retired datasets or unused clone origins.
- **Backup:** A replica that receives ongoing replication updates.
- **Match:** The most recent common snapshot (or source bookmark/target snapshot pair) between two replicas. If a match exists, incremental replication is possible.
- **Replica:** A copy of a dataset tree used for backup, archival, or failover. Zelta compares replication metadata to confirm whether replicas share usable history.
- **Savepoint:** Zelta's internal term for a bookmark or snapshot used as a replication reference point.
- **Source/Target:** The original dataset tree and its replica destination.

### ZFS Object Terms

- **Dataset (ds):** A ZFS filesystem or volume. In Zelta, this refers to the dataset itself, not its snapshots (unless explicitly stated).
- **Tree:** A dataset and all its children, processed recursively.
- **Endpoint (ep):** A complete dataset or snapshot reference, including optional username, hostname, pool, and path—similar to SCP syntax.
- **ds_suffix:** The relative path of a child dataset within a tree, with a leading `/`. For example, if replicating `zroot/usr` to `backup/usr`, a child at `zroot/usr/local` has a `ds_suffix` of `/local`. In scripting mode (`-H`), the top-level dataset has a `ds_suffix` of an empty string.

### Zelta Operations

- **Clone:** Create a temporary read-write copy of a dataset tree for recovery, testing, or inspection without disturbing the original. See `zelta-clone(8)`.
- **Rebase:** Rebase a dataset onto an upgraded upstream while preserving local files and incremental backup continuity. See `zelta-rebase(8)`.
- **Failover:** Lock an active source, perform a final backup, sync local ZFS properties, and unlock the promoted target.
- **Prune:** Plan snapshot pruning without destroying data. `zprune` performs the explicit destructive step after validation and preview.
- **Revert:** Safely roll back a dataset in place by renaming it and cloning from a previous snapshot. Unlike `zfs rollback`, this preserves your current state. See `zelta-revert(8)`.
- **Rotate:** Rename a replica and clone it back to the original name, then sync new history from the source. This handles divergent histories without destructive receives. See `zelta-rotate(8)`.

### Terms You Should Know

To get the most out of Zelta, you should be familiar with:

**ZFS Fundamentals:**
- Snapshots, bookmarks, and clones
- Filesystems and volumes
- Pools and properties
- Replication and delegation

**Disaster Recovery Concepts:**
- The difference between redundancy and backups
- Incremental vs. full backups
- Recovery Point Objective (RPO) and Recovery Time Objective (RTO)

Don't worry if you're new to these topics—Zelta's design makes many complex operations simple, and you'll learn as you go.

---

## How Zelta Works

Zelta is designed around three core principles: **safety**, **recursion**, and **remote operation**.

### Safe by Default

- **No destructive operations:** Zelta never suggests or requires `zfs receive -F` or forced rollbacks. Operations like `zelta rotate` preserve divergent versions rather than destroying them.
- **Read-only replicas:** Backups are created with `readonly=on` by default.
- **Mountpoint safety:** Child dataset mountpoints are reset to `inherit` to prevent dangerous overlapping mounts.
- **Pre-replication snapshots:** When needed, Zelta creates snapshots before replication to ensure backups are current.

### Recursive Operation

All Zelta commands work on entire dataset trees, not just individual datasets. When you replicate `tank/data`, you automatically get `tank/data/logs`, `tank/data/cache`, and everything underneath.

### Remote-First Design

Every Zelta command works the same way whether datasets are local or remote:

```sh
# Local to local
zelta backup tank/source tank/backup

# Local to remote
zelta backup tank/source backup@storage.example.com:pool/backup

# Remote to remote (orchestrated from your workstation)
zelta backup user@server1:tank/data user@server2:pool/backup
```

**You don't need to install Zelta on backup sources or targets.** Using SSH keys and agent forwarding, you can manage all replication from a secure bastion host.

### Non-Root Operation

**You never need to run Zelta as root.** Using ZFS delegation (`zfs allow`) and SSH keys, you can safely replicate datasets without privileged access. This dramatically reduces your attack surface and makes Zelta ideal for regulated environments.

See [ZFS Allow Delegation](/docs/conf/zfs-allow/) for setup instructions.

### Portable and Dependency-Free

Zelta runs on any system with Bourne shell and AWK—no packages, no daemons, no additional configuration required. Zelta can remotely manage backups between hosts that do not have Zelta installed.

### Environment Agnostic

Replication decisions are based on ZFS metadata and available features, not naming conventions or assumptions about your infrastructure. This makes Zelta an outstanding recovery tool for complex, mixed environments.

---

## Next Steps

Ready to try Zelta? Head over to [First Backup](/docs/start/) for practical examples.

For detailed command usage, run `zelta help` or explore the [Zelta documentation](https://zelta.space/docs/).
