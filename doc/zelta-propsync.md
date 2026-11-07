% zelta-propsync(8) | System Manager's Manual

# NAME
**zelta propsync** - sync local ZFS properties between dataset trees

# SYNOPSIS
**zelta propsync** [_OPTIONS_] _source_ _target_

# DESCRIPTION
The property synchronization step is documented under **zelta-failover(8)**. It replays local ZFS properties while preserving target-only local overrides before a promoted target takes over service.

# SEE ALSO
zelta-failover(8), zelta(8), zfs(8)

# AUTHORS
Daniel J. Bell <_bellhyve@zelta.space_>

# WWW
https://zelta.space
