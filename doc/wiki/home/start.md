# First Backup

This page is the short path from installation to a verified backup. For full setup details, see [Installation & Configuration](/docs/install/), [SSH Configuration](/docs/conf/ssh/), and [ZFS Allow Delegation](/docs/conf/zfs-allow/).

---

## Installation

### One-Shot Installer

```sh
# Latest release from main
curl -fsSL https://zelta.space/web-install.sh | sh
```

Run as root for a system install or as a backup user for a user-local install. No `git` required.

### From Source

```sh
git clone https://github.com/bell-tower/zelta.git
cd zelta
sudo ./install.sh
```

### FreeBSD Ports

Zelta is available in FreeBSD ports. Ports may lag the GitHub release; use the installer for current 1.2 features.

```sh
pkg install zelta
```

For detailed installation instructions and configuration options, see [Installation & Configuration](/docs/install/).

---

## Before You Begin

### Understanding Endpoints

Zelta uses an SCP-like syntax to specify datasets and snapshots:

**Format:** `[username@][hostname:]pool[/dataset][@snapshot]`

**Examples:**
- Local dataset: `tank/data`
- Remote dataset: `server10.biz:tank/files`
- Remote with user: `backup@server11.biz:pool/stuff`
- Remote snapshot: `twin@server12.biz:sink/log@today`

### Key Defaults

- **Recursive by default:** All operations work on entire dataset trees
- **Safe by default:** Replicas are created as `readonly=on`
- **Remote-first:** Every command works the same locally or remotely
- **No root required:** Use `zfs allow` for delegation

---

## Non-Root Operation

Zelta does not need to be installed on backup sources or targets—SSH keys and agent forwarding let you manage all replication from a single host. With ZFS delegation, there's no need to run Zelta as root either.

### Quick Setup

On source systems, delegate send permissions:
```sh
# As root, grant minimal permissions to backup user
zfs allow -u backupuser bookmark,hold,send:raw,snapshot sink/data
```

On target systems, delegate receive permissions:
```sh
# As root, grant receive permissions
zfs allow -u backupuser receive:append,create,mount,readonly,clone,rename,volmode,compression,recordsize tank/backups
```

The above uses modern OpenZFS delegation features. Use plain `send` or `receive` only when your platform lacks `send:raw` or `receive:append`, or when a separate high-trust role intentionally needs broader authority. See our detailed guides:
- [SSH Configuration](/docs/conf/ssh/)
- [ZFS Allow Delegation](/docs/conf/zfs-allow/)

---

## Basic Operations

### 1. Compare Two Dataset Trees

Before replicating, let's see what we're working with:

```sh
zelta match tank/data tank/backups/data
```

This shows the relationship between two dataset trees: matching snapshots, discrepancies, or whether one side is missing. It is the safest first command before backup or recovery work.

### 2. Create Your First Backup

Replicate a dataset tree to a local backup:

```sh
zelta backup tank/data tank/backups/data
```

**What just happened:**
- Zelta detected that the target doesn't exist and created it
- A snapshot was created on the source (if needed)
- The entire `tank/data` tree was replicated recursively
- The target was set to `readonly=on`
- The target mountpoints were reset to `inherit`

Run the same command again later to update incrementally. Zelta automatically detects the optimal `zfs send` method.

### 3. Replicate to a Remote System

The syntax is identical for remote replication:

```sh
zelta backup tank/data backup@storage.example.com:pool/backups/data
```

Zelta uses SSH to stream the replication. Make sure you've set up SSH keys and `zfs allow` permissions on both systems.

### 4. Verify the Replication

Confirm everything matches:

```sh
zelta match tank/data backup@storage.example.com:pool/backups/data
```

You should see matching snapshots across the entire tree.

---

## Automating Backups

For multiple backup jobs, use `zelta policy` with a config file instead of scheduling individual commands.

A minimal `zelta.conf`:

```yaml
SNAP_NAME: "$(date -u +auto-%Y-%m-%d_%H-%M)"

Production:
  app-server-01:
    - tank/www: tank/Backups/app-server-01/www
    - tank/database: tank/Backups/app-server-01/database
```

Then run all jobs:

```sh
zelta policy
```

Or target a specific site or host:

```sh
zelta policy Production
zelta policy app-server-01
```

Schedule it with cron to keep backups current automatically. For composable policies with shared rules and multiple targets, see [Policy Guide](/docs/guides/policy/) and the [centralized policy example](https://github.com/bell-tower/zelta/tree/main/examples/policy/centralized).

---

## Next Steps

After the first backup, expand from direct commands into policies and recovery testing.

### Learn More Commands
- `zelta revert`: Roll back a dataset in place without losing current state
- `zelta rotate`: Handle divergent histories without destructive receives
- `zelta clone`: Create temporary read-write copies for testing
- `zelta prune` and `zprune`: Plan and execute snapshot pruning as separate steps
- `zelta snapshot`: Create recursive snapshots on local or remote endpoints
- `zelta failover`: Promote a backup target safely
- `zelta rebase`: Move a production tree to an upgraded upstream while keeping backup continuity

Run `zelta usage` for quick command reference, or `zelta help` for the full manual.

### Advanced Topics
- [Installation & Configuration](/docs/install/): Detailed setup instructions
- [SSH Configuration](/docs/conf/ssh/): Secure remote replication setup
- [ZFS Allow Delegation](/docs/conf/zfs-allow/): Fine-grained permission management
- [Policy Guide](/docs/guides/policy/): Multi-job policy configuration
- [Backup Guide](/docs/guides/backup/): Backup and verification workflows
- [Retention Strategies](/docs/guides/retention/): Snapshot retention and prune safety
- [Failover Workflows](/docs/guides/sync/): Lock, final backup, property sync, and unlock

### Get Help

- **Documentation:** [zelta.space](https://zelta.space)
- **Issues & Features:** [GitHub Issues](https://github.com/bell-tower/zelta/issues)
- **General ZFS Support:** [PracticalZFS Community](https://discourse.practicalzfs.com)
- **Commercial Support:** [Bell Tower](https://belltower.it)
