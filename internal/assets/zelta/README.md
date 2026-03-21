![Zelta Logo](https://zelta.space/index/zelta-banner.svg)

# The Zelta Backup and Recovery Suite
*Version 1.1, 2026-01-20*

---

> - **What's New:** Check [CHANGELOG.md](CHANGELOG.md) for the latest changes
> - **Found a Bug?** Please [open an issue](https://github.com/bellhyve/zelta/issues)
> - **Previous Release:** [March 2024, Zelta v1.0](https://github.com/bellhyve/zelta/tree/release/1.0)

---

[zelta.space](https://zelta.space) | [Documentation](https://zelta.space/en/home) | [GitHub](https://github.com/bellhyve/zelta)

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

### From Source (Recommended for Zelta 1.1)
```sh
git clone https://github.com/bellhyve/zelta.git
cd zelta
sudo ./install.sh
# The installer will guide you through setup.
# For non-root installation, see install.sh output for user-mode variables.
```

### FreeBSD Ports
Zelta 1.0.1_1 (March 2024) is available in the FreeBSD Ports Collection. For the latest features, install from GitHub.
```sh
pkg install zelta
```

### Experimental: One-Shot Install
```sh
curl -fsSL https://raw.githubusercontent.com/bellhyve/zelta/main/contrib/install-from-git.sh | sudo sh
```

**Note:** This is experimental. For production use, we recommend cloning the repository and reviewing `install.sh` before running it.

---

## Quickstart: Developer Workflow

Zelta makes operations that are tricky or dangerous with raw commands straightforward and safe. Here's a real-world developer scenario showing backup, recovery, and time travel—all without destroying data.

### 1. Back Up Your Development Machine

Snapshot and back up your entire laptop to a backup server. Zelta handles snapshot creation, incremental detection, and safe transfers automatically.

```sh
# Syntax: zelta backup <user@host:source> <user@host:target>
zelta backup rpool backup-user@storage.example.com:tank/Backups/my-laptop
```

### 2. Back Up a Container as Non-Root

Grant a regular user minimal permissions to back up a specific dataset. This works well for containerized workloads, databases, or any delegated environment.

```sh
# As root, delegate send permissions on source
zfs allow -u developer send,snapshot,hold opt

# Delegate receive permissions on backup target
zfs allow -u developer receive:append,create,mount,canmount,volmode,readonly,clone,rename tank/Backups

# As developer user, back up
zelta backup opt/datasource/big-database-thing tank/Backups/big-database-thing
```

Run the same command again later to update incrementally. No configuration files, no daemon required.

### 3. Time Travel: Revert to Previous State

You need to roll back your development dataset to investigate a bug, but you don't want to lose your current environment.

```sh
# Rewind the working dataset in place, renaming it to preserve current state
zelta revert opt/datasource/big-database-thing
```

Your dataset is now at its previous snapshot. Your current work is still there, just renamed to `big-database-thing_zelta_2026-01-12`.

### 4. Keep the Backup Rolling After Divergence

Now your source has diverged from your backup. Normally this requires manual work or destructive receives. Not with Zelta.

```sh
# Rotate the backup: preserve the old version, receive the new history
zelta rotate opt/datasource/big-database-thing tank/Backups/big-database-thing
```

Done. You now have both versions preserved in your backup. No force flags, no data loss. This works between remote systems too.

---

## Core Tools

All Zelta commands operate recursively on backup sets and work locally or remotely via SSH.

### `zelta backup`
Robust backup with safe defaults. Creates consistent, read-only backups with intelligent incremental detection and optional pre-backup snapshots to ensure backups are current.

### `zelta match`
Compares two backup sets and reports matching snapshots or discrepancies. Essential for validating backups, planning rollbacks, and auditing.

### `zelta policy`
Automates large-scale concurrent backup operations across many systems using a policy-based engine. Supports hierarchical configuration with YAML-style policies.

### `zelta clone`
Creates temporary read-write clones of a backup set for zero-space iterative infrastructure, advanced development pipelines, recovery testing, or inspection—without disturbing the original. With `zelta clone`, there is never a reason to make your backup datasets writable.

### `zelta revert`
Carefully rewinds a dataset in place by renaming and cloning. Ideal for forensic analysis, testing, or recovering from mistakes without losing current state.

### `zelta rotate`
Performs a multi-way rename and clone operation to keep backups rolling even after source or target has diverged. Preserves all versions without destructive receives.

### `zelta prune` *(Experimental)*
Identifies snapshots eligible for deletion based on backup state and retention windows. Only suggests snapshots that are safely backed up to the target. Output is in range syntax for review before execution.

### Additional Commands

- `zelta snapshot`: Creates recursive snapshots on a local or remote endpoint.
- `zelta sync`: Runs `zelta backup -i` which skips intermediate snapshots.

Legacy shortcuts `zpull`, `zmatch`, and `zp` are supported when symlinked or aliased to `zelta`.

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

Zelta is open source under the BSD 2-Clause License and will always remain permissively licensed.

We welcome contributors who are passionate about data protection and recovery. By contributing to Zelta, you help make reliable backup and recovery accessible to everyone.

### Contact

We welcome questions, bug reports, and feature requests at [GitHub Issues](https://github.com/bellhyve/zelta/issues).

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

## Roadmap

Zelta 1.1 represents a major refactor that improves POSIX compliance, portability, and code maintainability. The following features are already used internally or by Bell Tower clients and will be upstreamed by Q2 2026.

### Features In Development

- **zelta lock/unlock**: Simplify failover by confirming the correct twin is read-only before promoting a read-write primary.
- **zelta rebase**: Update base images across filesystems while preserving customizations.
- **zelta prune**: Improve to identify snapshots based on additional metadata-driven policies such as snapshot density and usage patterns.
- **Metadata-Aware Sync Protection**: Ensure backup continuity using automatic holds and bookmarks based on backup relationships, and track property changes with user properties.
- **Flexible API**: Although `zelta backup` has a JSON output mode useful for telemetry, we intend to match native JSON output styles for more integration options. To support larger fleets, `zelta policy` configurations are being updated to support JSON, SQLite, and other database formats.
