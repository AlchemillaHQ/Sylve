# Zelta Backup and Recovery Suite

## Documentation Index

Zelta provides safe ZFS backup and recovery operations that remain straightforward to deploy and operate.

Zelta orchestrates backup operations across modern Unix systems. It has zero package dependencies and can run from an orchestrator without being installed on the ZFS endpoints.

> **Zelta requires ZFS.** If ZFS isn't yet part of your workflow, see [Getting Started with ZFS](/docs/conf/zfs/) to begin.



## Getting Started



Learn about Zelta and get your first backups running.

- **[About Zelta](/docs/about/):** Project overview and design philosophy.
- **[Definitions & Features](/docs/overview/):** Endpoints, dataset trees, and backup operations.
- **[First Backup](/docs/start/):** Create a backup with `zelta backup` and verify it with `zelta match`.
- **[Installation & Configuration](/docs/install/):** Deploy, configure, and customize Zelta for your server or fleet.
- **[FAQ](/docs/faq/):** Answers to common questions.



## Core Tools



All Zelta commands operate recursively on dataset trees and work locally or remotely via SSH.

See the complete [Manual Pages](/docs/man/) index for command references.

- **[Manual: zelta](/docs/man/zelta/):** Overview of the Zelta suite and common options.
- **[Manual: zelta backup](/docs/man/zelta-backup/):** Backup with safe defaults, incremental detection, and optional pre-backup snapshots.
- **[Manual: zelta match](/docs/man/zelta-match/):** Compare dataset trees and report matching snapshots or discrepancies.
- **[Manual: zelta policy](/docs/man/zelta-policy/):** Automate concurrent backup operations using policy-based configuration.
- **[Manual: zelta clone](/docs/man/zelta-clone/):** Create temporary read-write clones for testing, recovery, or inspection.
- **[Manual: zelta revert](/docs/man/zelta-revert/):** Rewind a dataset in place by renaming and cloning, preserving current state.
- **[Manual: zelta rotate](/docs/man/zelta-rotate/):** Keep backups rolling after divergence by preserving all versions.
- **[Manual: zelta snapshot](/docs/man/zelta-snapshot/):** Create recursive snapshots on local or remote endpoints.
- **[Manual: zelta prune](/docs/man/zelta-prune/):** Plan snapshot pruning without destroying data.
- **[Manual: zprune](/docs/man/zprune/):** Validate and destroy snapshots selected by `zelta prune`.
- **[Manual: zelta failover](/docs/man/zelta-failover/):** Promote a backup target through a guarded failover workflow.
- **[Manual: zelta rebase](/docs/man/zelta-rebase/):** Rebase a dataset onto an upgraded upstream while preserving local files and backup continuity.
- **[Guide: Zelta Twin](/docs/guides/twin/):** Compose asynchronous cluster pairs from reciprocal backup policy and guarded failover commands.



## Guides

See the complete [Guides](/docs/guides/) index.


- **[Simple Backups](/docs/guides/backup/):** Create and verify direct backups.
- **[Policy-Based Automatic Backups](/docs/guides/policy/):** Run multiple backup jobs from configuration.
- **[Zelta Twin](/docs/guides/twin/):** Asynchronous cluster pairs and guarded failover.
- **[Failover Workflows](/docs/guides/sync/):** Backup, lock/unlock, propsync, and failover.
- **[Rollback & Recovery](/docs/guides/recovery/):** Snapshots, clones, revert, and rotate.
- **[Retention Strategies](/docs/guides/retention/):** Plan snapshot retention and safe destruction.
- **[JSON Output](/docs/guides/json/):** Machine-readable output for monitoring.



## Configuration Examples

See the complete [Configuration](/docs/conf/) index.


- **[Environment & Policy Files](/docs/conf/env/):** Which file to edit, syntax, and precedence.
- **[Configuration: ssh](/docs/conf/ssh/):** SSH best practices for efficient ZFS backup.
- **[Configuration: zfs allow](/docs/conf/zfs-allow/):** Set up ZFS permissions with minimal access.
- **[Configuration: zelta.conf](/docs/conf/zelta-conf/):** Policy configuration for complex backups.
- **[Configuration: zelta.env](/docs/conf/zelta-env/):** Override default behavior, snapshot names, and `zfs` options.
- **[Configuration: zfs](/docs/conf/zfs/):** Getting started with ZFS for new users.



### [Next Page: About](/docs/about/)
