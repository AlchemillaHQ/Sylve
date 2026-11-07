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

**-n**, **\--dryrun**
:    Show a table of source and target endpoints that would be processed, then exit. No backup jobs are run. See **Dry Run Output** below.

**-H**
:    Scripting mode: suppress the **SOURCE**/**TARGET** header and separate columns with a single space. Intended for pipeline use. Only meaningful with **--dryrun**.

**-C**, **\--config** _FILE_
:    Read policy configuration from _FILE_ instead of the default location.

**\--backup-root**
:    The relative target path for the target job. For example 'bkhost:tank/Backups' would place backups below that dataset (if not overridden).

**\--archive-root**
:    Default archive target root for policy jobs that use archive-style destinations.

**\--backup-command** _COMMAND_
:    Command used by policy when executing backup jobs. This is primarily useful for wrappers and testing.

**\--host-prefix**
:    Include the source hostname as a parent of the synced target, for example, 'tank/Backups/source.host/backup-dataset'.

**\--ds-prefix**
:    Similar to 'zfs recv -d' and '-e', include the indicated number of parent labels for the target's backup name. See **zelta-options(7)** for more detail.

## Import Files

Policy files may use `import:` to insert another local policy fragment before parsing. Import paths are resolved relative to the file that contains the `import:` line, which allows a policy directory to be moved as a unit.

```yaml
SITE0:
  host1.example:
    options:
      import: targets/vault1.yaml
      import: rules/hostbackup.yaml
    datasets:
      import: sources/host1.example.yaml
```

Imported files are textual fragments, not independent policy files. The indentation of the `import:` line is prepended to each imported line, so fragments should usually contain only the lines needed inside the current context. For example, a dataset inventory fragment can contain only list items:

```yaml
- tank/vm/app1
- tank/vm/app2
```

Imports are expanded recursively up to a fixed depth limit, and recursive import loops are rejected. Later options override earlier options, but unspecified options remain in effect within the current policy context.

## Dry Run Output

**zelta policy --dryrun** prints a column-aligned table of fully-resolved source and target endpoints, one row per backup job, then exits without running any jobs:

```
SOURCE                                     TARGET
compute1.nyc1:ssd09/jail/bellmgmt_bts      vault1.den0:data1/Backups/bellmgmt_bts
compute1.nyc1:ssd09/jail/bellmgmt_bts      vault1.nyc1:rust12/Backups/bellmgmt_bts
```

Column width is computed dynamically from the longest source endpoint. When a single source fans out to multiple targets, each target appears as a separate row.

Adding **--verbose** (`-v`) switches to raw **zelta backup** command output suitable for manual execution or debugging. Policy-internal environment variables (**ZELTA\_LOG\_PREFIX**, **ZELTA\_LOG\_MODE**, **ZELTA\_LOG\_LEVEL**, **ZELTA\_LOG\_COMMAND**) are stripped from each command since they are only meaningful within the policy process.

Adding **-H** suppresses the **SOURCE**/**TARGET** header and separates columns with a single space, making the output suitable for scripting:

```
compute1.nyc1:ssd09/jail/bellmgmt_bts vault1.den0:data1/Backups/bellmgmt_bts
compute1.nyc1:ssd09/jail/bellmgmt_bts vault1.nyc1:rust12/Backups/bellmgmt_bts
```

All three modes compose with operand filtering. For example, `zelta policy -n bellmgmt_bts` shows only the rows matching that dataset.

## Backup Job Parameters
Without additional parameters, **zelta policy** will run a **zelta backup** job for each dataset in the configuration file. Providing one or more operands limits processing to matching jobs.

Each operand is matched against the following axes for each configured job:

**_site_**
:    The user-defined top-level grouping in the configuration file. Selects all jobs belonging to that site.

**_host_**
:    The source hostname. Selects all jobs originating from that host.

**_dataset_**
:    Matches against the source dataset path, the target dataset path, or the final label (leaf) of either. For example, `bellmgmt_bts` matches any job whose source or target ends in that name.

**_host:dataset_**
:    The fully-qualified source or target endpoint. Equivalent to the endpoint format used by **zelta backup**.

Multiple operands are matched with OR logic: a job is included if it matches any operand. Operands are not matched against **BACKUP\_ROOT** or other template values — they match against the fully-resolved endpoint paths that would be passed to **zelta backup**.

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
