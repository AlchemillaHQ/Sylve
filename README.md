![Zelta Logo](https://zelta.space/index/zelta-banner.svg)
# The Zelta Backup and Recovery Suite
*Current release: 1.2.0*

---
> - **What's New:** Check [CHANGELOG.md](CHANGELOG.md) for the latest changes
> - **Found a Bug?** Please [open an issue](https://github.com/bell-tower/zelta/issues)
> - **Previous Release:** [March 2024, Zelta v1.0](https://github.com/bell-tower/zelta/tree/release/1.0)
> 
>&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;  ![ShellSpec Tests](https://github.com/bell-tower/zelta/actions/workflows/shellspec.yml/badge.svg)
---

[zelta.space](https://zelta.space) | [Documentation](https://zelta.space/docs/) | [GitHub](https://github.com/bell-tower/zelta)

**Zelta** provides bulletproof backups that meet strict compliance requirements while remaining straightforward to deploy and operate. It transforms complex backup and recovery operations into safe, auditable commands—protecting your data without requiring specialized expertise.

Zelta orchestrates backup operations across any modern Unix system. It has been battle-tested in production for over six years, managing tens of millions of snapshots across thousands of systems—with zero package dependencies.

> **Zelta requires ZFS.** If ZFS isn't yet part of your workflow, it's easier than ever to improve your infrastructure with ZFS and Zelta. See [Getting Started with ZFS](https://zelta.space/conf/zfs) to begin.

---

## Why Zelta?

### Compliance-Ready by Design
Zelta preserves every version of your data without destructive overwrites. When source and backup diverge, Zelta keeps both versions intact. Your team is already telling auditors you do this—Zelta makes it practical and verifiable.

### Safe Defaults, No Surprises
Backups are created read-only. Dangerous operations are rejected, not just discouraged. Zelta has no destructive features and never requires force flags to function correctly.

### Zero Footprint on Endpoints
Zelta runs entirely from a management host using SSH. Your backup sources and targets need only standard system tools—no agents, no daemons, no additional attack surface.

### Portable and Dependency-Free
Written in portable Bourne shell and AWK, Zelta runs anywhere ZFS runs. No package managers, no runtime dependencies, no version conflicts.

---

## Installation

### One-Shot Installer

Run as root for a system install or as a backup user for a user-local install. No `git` required.

```sh
# Latest release from main
curl -fsSL https://zelta.space/web-install.sh | sh
```

The installer uses sane defaults for system-wide or user installs. Advanced install paths can be overridden with `ZELTA_BIN`, `ZELTA_SHARE`, `ZELTA_ETC`, and `ZELTA_DOC`; see the install documentation for details.

*Security Note: As with any script piped from the internet, inspect the [installer source](https://github.com/bell-tower/zelta/blob/main/contrib/web-install.sh) before execution.*

### From Source
```sh
git clone https://github.com/bell-tower/zelta.git
cd zelta
sudo ./install.sh
# The installer will guide you through setup.
# For non-root installation, see install.sh output for user-mode variables.
```

### FreeBSD Ports
Zelta is available in the FreeBSD Ports Collection. Ports may lag the GitHub release; use the installer for current 1.2.0 features.
```sh
pkg install zelta
```

---

## First Backup

Zelta commands use endpoint syntax familiar from **scp(1)**:

```text
[user@][host:]pool/dataset[@snapshot]
```

Compare a source and target before backing up:

```sh
zelta match rpool/data backup-user@storage.example.com:tank/Backups/data
```

Create or update the backup:

```sh
zelta backup rpool/data backup-user@storage.example.com:tank/Backups/data
```

Run the same command again later to update incrementally. For non-root operation, delegate ZFS permissions with `zfs allow`; see the SSH and ZFS delegation guides for complete examples.

---

## Core Tools

All Zelta commands operate recursively on backup sets and work locally or remotely via SSH.

### `zelta backup`
Robust backup with safe defaults. Creates consistent, read-only backups with intelligent incremental detection and optional pre-backup snapshots to ensure backups are current.

### `zelta match`
Compares two backup sets and reports matching snapshots or discrepancies. Essential for validating backups, planning rollbacks, and auditing.

### `zelta policy`
Runs multiple backup jobs concurrently from a single configuration file. Policies are hierarchical: global settings, site, host, and dataset-level options cascade to specific jobs.

### `zelta clone`
Creates temporary read-write clones of a backup set for recovery testing, inspection, or development—without disturbing the original. There is never a reason to make your backup datasets writable.

### `zelta revert`
Carefully rewinds a dataset in place by renaming and cloning. Ideal for forensic analysis, testing, or recovering from mistakes without losing current state.

### `zelta rotate`
Performs a multi-way rename and clone operation to keep backups rolling even after source or target has diverged. Preserves all versions without destructive receives.

### `zelta prune`
Plans snapshot pruning without destroying data. Candidate selection is separate from destruction and can use time, count, grid, reclaim-size, name, policy, and guard controls.

### `zprune`
Destructive companion for `zelta prune`. Validates prune candidates, previews `zfs destroy -nvp`, groups transactions, and prompts before destroying snapshots.

### `zelta failover`
Locks an active source, performs a final backup, syncs local ZFS properties, and unlocks the promoted target.

### `zelta rebase`
Rebase a dataset onto an upgraded upstream while preserving local files and incremental backup continuity.

### `zelta lock` and `zelta unlock`
Apply ordered dataset-tree readonly, canmount, unmount, and remount workflows for promotion and maintenance.

### `zelta propsync`
Replays local ZFS properties from one dataset tree to another while preserving target-only local overrides.

### Additional Commands

- `zelta snapshot`: Creates recursive snapshots on a local or remote endpoint.

Compatibility aliases such as `zelta sync`, `zpull`, `zmatch`, and `zp` are supported for existing operators. New documentation uses explicit `zelta backup` commands.

---

## Safety by Design

Zelta prioritizes data integrity above all else. Safety is built into every design decision.

### Safe Defaults
- Backups are created as read-only by default
- Child dataset mountpoints are reset to prevent dangerous overlapping mounts
- Snapshots are created before backup when needed to ensure up-to-date backups

### No Forced Overwrites
Zelta never suggests or requires destructive actions. The `zelta rotate` feature preserves divergent datasets by cloning before receiving new history.

### Remote and Recursive
All operations work remotely and recursively by default. Zelta backs up as much as possible and reports clearly about any discrepancies.

### Environment Agnostic
Backup decisions are based on metadata and available features, not naming conventions. This makes Zelta an effective recovery tool for complex, mixed environments.

### Minimal Attack Surface
Zelta can run entirely from a bastion host using SSH keys or agent forwarding. The Zelta team at Bell Tower runs its core backup loop from a locked-down system with configurations ensuring that no backup user has access to any unencrypted dataset throughout the entire workflow.

---

## Community & Support

Zelta is open source under the BSD 2-Clause License and will always remain permissively licensed. Contributions welcome.

### Contact

We welcome questions, bug reports, and feature requests at [GitHub Issues](https://github.com/bell-tower/zelta/issues).

For other inquiries including business questions, you can reach the Zelta team at Bell Tower via our [contact form](https://belltower.it/contact/).

### Conference Talks

**BSDCan 2024: Zelta: A Safe and Powerful Approach to ZFS Replication** 
By Daniel J. Bell 
[Watch on YouTube](https://www.youtube.com/watch?v=_nmgQTs8wgE)

**OpenZFS Summit 2025: Responsible Replication with Zelta** 
[Watch on YouTube](https://www.youtube.com/watch?v=G3weooQqcXw)

### Bell Tower Services

For commercial support, custom feature development, and consulting on secure, high-efficiency infrastructure, contact us at [Bell Tower](https://belltower.it/). We provide consulting services for advanced policy management, cost control, compliance, and business continuity.

---

## Current Direction

Zelta 1.2.0 adds the prune planner/`zprune` split, rebase, failover, lock/unlock, propsync, snapshot thresholds, policy imports, replication-success bookmarks, and broader include/exclude filtering. See [CHANGELOG.md](CHANGELOG.md) for release details and current known issues.
