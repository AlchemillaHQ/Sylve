# Policy-Based Automatic Backups

`zelta policy` runs multiple `zelta backup` jobs from `zelta.conf`. User configuration under `~/.config/zelta` is used when present; otherwise Zelta falls back to `/usr/local/etc/zelta`.

```yaml
BACKUP_ROOT: backup@storage.example.com:tank/Backups
JOBS: 2

Production:
  app1.example.com:
    - tank/www
    - tank/database
  app2.example.com:
    - tank/www
```

Run every configured job:

```sh
zelta policy
```

Limit a run by site, host, dataset, or endpoint:

```sh
zelta policy Production
zelta policy app1.example.com
zelta policy tank/database
```

Large policies can use `import:` fragments for sources, targets, and shared rules. Import paths are resolved relative to the file containing the `import:` line and recursive import loops are rejected.

```yaml
Production:
  app1.example.com:
    options:
      import: rules/production.yaml
    datasets:
      import: sources/app1.yaml
```

## Bastion Composition

A common production pattern is to run Zelta from a locked-down bastion account. The bastion holds SSH keys or an SSH agent socket, policy files, and no root credentials. The ZFS endpoints need only SSH and ZFS.

Organize large policies as reusable fragments:

```text
~/.config/zelta/
  zelta.conf
  rules/
    hostbackup.yaml
  sources/
    app1.yaml
    app2.yaml
  targets/
    backup-a.yaml
    backup-b.yaml
```

Example target fragment:

```yaml
BACKUP_ROOT: backup-a.example.com:tank/backups
```

Example rule fragment:

```yaml
ADD_HOST_PREFIX: 1
```

Example source fragment:

```yaml
- tank/jail/app
- tank/vm/database
```

Top-level policy lanes can represent sites, routes, backup waves, or any other operational grouping:

```yaml
RETRY: 2
JOBS: 4
SNAP_TIME: 8h
SEND_INTR: 0
EXCLUDE: /backups,/swap,/tmp

DAL1_TO_BACKUP_A:
  app1.example.com:
    options:
      import: targets/backup-a.yaml
      import: rules/hostbackup.yaml
    datasets:
      import: sources/app1.yaml

DAL1_TO_BACKUP_B:
  app1.example.com:
    options:
      import: targets/backup-b.yaml
      import: rules/hostbackup.yaml
    datasets:
      import: sources/app1.yaml
```

This keeps policy readable without turning Zelta into a server. Zelta still lowers each entry to ordinary `zelta backup` jobs.

Always test policy changes with:

```sh
zelta policy -n
```

For reciprocal failover policy, see [Zelta Twin](/docs/guides/twin/).
