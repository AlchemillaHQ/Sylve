% zelta-policy(8) | System Manager's Manual

# NAME
**zelta policy** - execute a list of **zelta backup** operations from a policy configuration

# SYNOPSIS
**zelta policy** [_backup-override-options_] [_site_|_host_|_dataset_] _..._

# DESCRIPTION
**zelta policy** reads from a simple YAML-style configuration file containing parameters for **zelta backup**. Running **zelta policy** without parameters will iterate through the entire configuration file. Override options for either **zelta policy** or **zelta backup** can be provided on the command line, for example, adding `--json` will cause that parameter to be passed to all **zelta backup** calls. Subsequent undashed parameters will be matched against a user-defined _site_ name, _host_ name, or _dataset_, and the backup processes will be limited to that group or item.

## Options
In the **zelta policy** configuration file, you may override **zelta backup**'s default parameters as defined in **zelta-backup(8)**. For the complete list of Zelta configuration options, see **zelta-options(7)**. The following parameters are specific **zelta policy**:

**\--retry**
:    Retry failed syncs the indicated number of times.

**\--jobs**
:    Run the indicated number of policy jobs concurrently, one for each Site in the configuration.

**\--backup-root**
:    The relative target path for the target job. For example 'bkhost:tank/Backups' would place backups below that dataset (if not overridden).

**\--host-prefix**
:    Include the source hostname as a parent of the synced target, for example, 'tank/Backups/source.host/backup-dataset'.

**\--ds-prefix**
:    Similar to 'zfs recv -d' and '-e', include the indicated number of parent labels for the target's synced name. See 'zelta help backup' for more detail.

## Backup Job Parameters
Without additional parameters, **zelta policy** will run a **zelta backup** job for each dataset in the configuration file. Providing one of the following will limit the backup job.

**_site_**  Run a backup job only for the _site_ listed. A _site_ is a user defined top-level parameter in the configuration file representing a list of one or more hosts.

**_host_**  Run a backup job only for the _host_ listed. Hosts must be accessible via SSH private-key authentication or **localhost**.

**_dataset_**  Run a backup job only for the _dataset_ listed. Note this parameter can match the _source_ **or** _target_ dataset, e.g., requesting `zroot` would run the replication for any matching dataset on any host.

**_host:dataset_**  Specify a _source_ or _backup_ dataset endpoint name, equivalent to the parameters of **zelta backup**.

**_dataset_pattern_**  Specify the final source or target dataset label. For example, `vm` would run all backup jobs with datasets ending in `/vm`.

# FILES
For detailed documentation of the **zelta policy** configuration see `zelta.conf.example`.

**/usr/local/etc/zelta/zelta.conf**
:    The default configuration file location.

# ENVIRONMENT
For detailed documentation of the **zelta** environment variables see `zelta help options`.

# EXIT STATUS

Returns 0 on success, non-zero on error.

# NOTES

See **zelta-options(7)** for environment variables and `zelta.env` configuration.

# SEE ALSO
zelta(8), zelta-clone(8), zelta-backup(8), zelta-options(7), zelta-match(8), zelta-revert(8), zelta-rotate(8), zelta-snapshot(8), ssh(1), zfs(8), zfs-list(8)

# AUTHORS
Daniel J. Bell <_bellhyve@zelta.space_>

# WWW
https://zelta.space
