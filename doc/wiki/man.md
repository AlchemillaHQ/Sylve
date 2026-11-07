# Zelta Manual Pages

## Command Reference

### [zelta(8)](/docs/man/zelta/)
Main Zelta command interface and controller

### [zelta-options(7)](/docs/man/zelta-options/)
Common options and configuration for Zelta commands

### [zelta-match(8)](/docs/man/zelta-match/)
Compare datasets and report matching snapshots or discrepancies

### [zelta-backup(8)](/docs/man/zelta-backup/)
Create and update ZFS backups with robust replication

### [zelta-policy(8)](/docs/man/zelta-policy/)
Run configured backup jobs using policy-based automation

### [zelta-clone(8)](/docs/man/zelta-clone/)
Clone ZFS datasets for testing or recovery

### [zelta-revert(8)](/docs/man/zelta-revert/)
Rename and clone a dataset in-place to rewind state

### [zelta-rotate(8)](/docs/man/zelta-rotate/)
Recover sync continuity after divergence

### [zelta-snapshot(8)](/docs/man/zelta-snapshot/)
Create recursive snapshots on local or remote endpoints

### [zelta-prune(8)](/docs/man/zelta-prune/)
Plan snapshot pruning without destroying data

### [zprune(8)](/docs/man/zprune/)
Validate and destroy snapshots selected by `zelta prune`

### [zelta-failover(8)](/docs/man/zelta-failover/)
Promote a backup target through a guarded failover workflow

### [zelta-rebase(8)](/docs/man/zelta-rebase/)
Build a new dataset tree from an upgraded upstream while preserving backup continuity

The failover manual also documents the lower-level `zelta lock`, `zelta unlock`, and `zelta propsync` steps.
