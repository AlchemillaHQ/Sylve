% zelta-failover(8) | System Manager's Manual

# NAME
**zelta failover** - promote a backup target through a guarded ZFS failover workflow

# SYNOPSIS
**zelta failover** [_OPTIONS_] _source_ _target_

# DESCRIPTION
**zelta failover** composes the normal active-passive promotion workflow for a dataset tree. It locks the active source, performs a final backup, syncs local ZFS properties, and unlocks the promoted target.

The command is intended for planned promotion and controlled failback workflows where the standby dataset tree is already maintained by **zelta backup**. It does not make split-brain safe by itself; operators must still ensure only one side is active and writable.

When reciprocal **zelta policy** jobs maintain both directions, this is the Zelta Twin pattern: an asynchronous cluster pair where either dataset tree can become active after a guarded failover.

The individual commands are also useful outside the complete failover workflow. The complete command reference follows below.

## Composable Administrative Commands

The failover workflow is composed from several useful administrative commands.
Use **zelta failover** when you want the complete guarded promotion, or use the
individual commands when you need to inspect, repeat, or combine one operation
with another administrative workflow.

### **zelta lock**

**zelta lock** applies ordered readonly, canmount, unmount, and remount operations to a dataset tree. It is useful after a partial failover, before maintenance, or whenever a dataset tree needs to be made safely inactive.

**zelta lock** _endpoint_

**-f**, **\--force**
: Force unmounts during lock.

**\--no-unmount**
: Set readonly and canmount state but leave mounted filesystems mounted.

```sh
zelta lock primary.example.com:tank/service
```

### **zelta propsync**

**zelta propsync** replays local ZFS properties from one dataset tree to another while preserving target-only local overrides. It can prepare a promoted target to take over service, or make the properties of an unrelated dataset tree match another tree.

**zelta propsync** _source_ _target_

```sh
zelta propsync primary.example.com:tank/service standby.example.com:tank/service
```

### **zelta unlock**

**zelta unlock** reverses the lock state so a promoted dataset tree can become active. It is also useful when bringing a cloned or otherwise prepared dataset tree online in a controlled way.

**zelta unlock** _endpoint_

```sh
zelta unlock standby.example.com:tank/service
```

# OPTIONS

_source_
: Active dataset tree to lock and back up before promotion.

_target_
: Standby dataset tree to receive the final backup and become writable.

**-n**, **\--dryrun**, **\--dry-run**
: Display underlying commands without executing them.

**-v**, **\--verbose**
: Increase verbosity. Specify once for operational detail, twice for debug output.

**-q**, **\--quiet**
: Decrease log output.

**-f**, **\--force**
: Force source unmounts during the lock phase.

**\--no-unmount**
: Lock the source read-only but leave mounted filesystems mounted.

Backup transport, send, receive, resume, bookmark, and snapshot naming options accepted by **zelta backup** may also be passed to **zelta failover**. The failover command uses them for the final backup step; see **zelta-backup(8)** for details.

# EXAMPLES

Promote a standby service tree:

    zelta failover primary.example.com:tank/service standby.example.com:tank/service

Promote using source-initiated remote transport:

    zelta failover --push primary.example.com:tank/service standby.example.com:tank/service

Perform the same workflow explicitly:

    zelta lock primary.example.com:tank/service
    zelta backup primary.example.com:tank/service standby.example.com:tank/service
    zelta propsync primary.example.com:tank/service standby.example.com:tank/service
    zelta unlock standby.example.com:tank/service

# EXIT STATUS
Returns 0 on success, 1 if non-critical mount or unmount operations fail after the state transition continues, 2 if the final backup fails after source lock, and 255 if Zelta cannot safely set or clear dataset readonly state.

Unmount failures on the locked source and mount failures on the unlocked target are reported but do not stop failover. Readonly and final backup failures stop immediately.

# NOTES

Always verify state with **zelta match** before and after promotion. Do not run both sides read-write at the same time.

# SEE ALSO
zelta(8), zelta-backup(8), zelta-match(8), zelta-policy(8), zelta-options(7), ssh(1), zfs(8)

# AUTHORS
Daniel J. Bell <_bellhyve@zelta.space_>

# WWW
https://zelta.space
