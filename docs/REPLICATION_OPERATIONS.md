# Replication operations

TODO: Hayzam: This should be moved into the main documentation linked from the README, I haven't gotten around to writing it yet but putthing it here now so I don't forget/this gets lost.

## Failover modes

- **Manual** never moves a workload because its active node is unavailable. An administrator chooses the target and whether to use a safe move or force recovery.
- **Automatic safe handoff** acts only while the active node is reachable and can acknowledge demotion and final synchronization. If that node is unreachable, Sylve records one warning for the outage and waits for the node to return or for an administrator to force recovery.
- **Automatic force recovery** may promote a complete replicated generation after node loss without a demotion acknowledgement. The newest writes that never reached the selected target may be lost.

Forced recovery prefers the freshest complete generation. Target priority is used for normal selection and to break freshness ties.

## Pool-health actions

Pool-health monitoring is an active recovery feature, not alerting alone:

- capacity pressure requests a safe handoff;
- an unhealthy pool requests force recovery and may lose the newest writes.

Disable pool-health monitoring when external alerting and manual recovery are preferred.

## Emergency local fencing

Replication deliberately fails closed when a node cannot read local replication policy state or cannot determine its local node identity. In that condition Sylve:

1. stops every Sylve-managed VM and jail on the node, including workloads without a replication policy;
2. fences canonical guest datasets to prevent writes while ownership is unknown;
3. retains the last durable ownership observations so normal dataset properties can be restored after control-plane recovery.

This is intentionally broader than the set of currently protected guests because an unreadable policy database cannot prove which guests are protected.

### Recovery procedure

1. Restore local database access, node identity, and Raft/control-plane connectivity.
2. Confirm that replication self-fence checks complete without errors and that each protected workload shows the expected active owner and lease epoch.
3. Verify target readiness and any pending transition before starting workloads.
4. Allow Sylve to reconcile dataset fencing. Do not manually clear `readonly` while ownership is still uncertain.
5. Manually start only the VMs and jails that should be running. Emergency fencing does not automatically restart unrelated workloads.

If ownership cannot be established, leave the workload stopped and perform an explicit administrator-directed force recovery from the intended target.
