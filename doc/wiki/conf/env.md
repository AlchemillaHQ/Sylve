# Environment & Policy Files

As you move from manual backups to automated fleets, you shouldn't have to keep retyping the same arguments. Zelta uses two optional configuration files:

- `zelta.env` — global defaults for all Zelta commands, in Bourne shell variable format.
- `zelta.conf` — backup job definitions for `zelta policy`, in YAML-like policy format.

For the complete option reference, see [zelta-options(7)](/docs/man/zelta-options/) or run `zelta help options`.

---

## Syntax at a glance

`zelta.env` uses Bourne shell syntax:

```sh
# ~/.config/zelta/zelta.env or /usr/local/etc/zelta/zelta.env
# This example sets a preferred snapshot naming scheme,
# enables SSH agent forwarding, and globally enables "verbose" mode.
SNAP_NAME='$(date -u +manual_backup_%Y-%m-%d_%H.%M.%S)'
REMOTE_SEND="ssh -An"
LOG_LEVEL=3
```

`zelta.conf` uses YAML-like policy syntax:

```yaml
# ~/.config/zelta/zelta.conf or /usr/local/etc/zelta/zelta.conf
# This example configures 'zelta policy' to back up two dataset
# trees to its backup target.
BACKUP_ROOT: backup.example.com:tank/Backups
JOBS: 2

NYC1:
  host1.example.com:
  - zroot/jails/web
  - zroot/jails/db
```

Options are supported in both contexts, but `zelta policy` options only influence their level of the policy hierarchy.

---

## Configuration Hierarchy

Zelta options follow a "specific beats general" hierarchy:

1. Built-in defaults
2. `zelta.env` global defaults
3. `zelta.conf` policy settings, for `zelta policy` only
4. `ZELTA_*` environment variables from the shell, cron, or scripts
5. Command-line arguments

For example, `zelta policy --no-snapshot` overrides snapshot settings from both `zelta.env` and `zelta.conf` for that run. That's a common use case, as you may wish to use `zelta policy` to check recent backup cronjobs without creating extra snapshots datasets.

---

## Global Defaults: `zelta.env`

Use `zelta.env` for settings that should apply broadly, such as SSH commands, logging level, output mode, snapshot naming, send flags, receive flags, and retention defaults.

Default locations:

- User: `~/.config/zelta/zelta.env` when present
- System: `/usr/local/etc/zelta/zelta.env`

Environment variables are supported outside `zelta.env`, but must include the `ZELTA_` prefix:

```sh
export ZELTA_LOG_LEVEL=4
export ZELTA_REMOTE_COMMAND="ssh -p 2202"
zelta backup pool/data backup.example.com:tank/Backups/data
```

Some startup variables must be exported before Zelta can find its files, because they are needed before `zelta.env` is loaded:

- `ZELTA_AWK`
- `ZELTA_ETC`
- `ZELTA_ENV`
- `ZELTA_CONFIG`
- `ZELTA_SHARE`

For the complete option list, run `zelta help options` or see `zelta-options(7)`.

---

## Policy Jobs: `zelta.conf`

Use `zelta.conf` only for `zelta policy`. It records which source datasets should be backed up, where they should go, and which options apply to those jobs.

Default locations:

- User: `~/.config/zelta/zelta.conf` when present
- System: `/usr/local/etc/zelta/zelta.conf`

Policy files are organized as site, host, and dataset definitions:

```yaml
BACKUP_ROOT: backup.example.com:tank/Backups
RETRY: 2

PROD:
  web1.example.com:
  - zroot/jails/nginx
  - zroot/jails/php

  db1.example.com:
    options:
      SNAP_MODE: ALWAYS
      ADD_HOST_PREFIX: 1
    datasets:
    - zroot/db/postgres
```

Policy files may also use `import:` to compose local fragments. Imports are resolved relative to the file that contains the `import:` line, expanded recursively with loop protection, and are useful for splitting source inventories, target definitions, and shared rules.

For policy structure and selection rules, run `zelta help policy` or see `zelta-policy(8)`.

---

## Validate Before Running

Parse the policy and print the job list without connecting to hosts:

```sh
zelta policy -n
```

Run a verbose dry run:

```sh
zelta policy -v -n
```

Run one site, host, or dataset from the policy:

```sh
zelta policy PROD
```

---

## Next Steps

- [Configuration: zelta.env](/docs/conf/zelta-env/) - Global defaults file
- [Configuration: zelta.conf](/docs/conf/zelta-conf/) - Policy job file
- [Policy Guide](/docs/guides/policy/) - Build and test policy jobs
- [SSH Configuration](/docs/conf/ssh/) - Remote backup setup
- [ZFS Allow Delegation](/docs/conf/zfs-allow/) - Non-root permission management

For complete option details, use `zelta help options` or `zelta-options(7)`.
