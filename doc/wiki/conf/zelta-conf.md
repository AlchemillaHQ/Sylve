# Configuration: `zelta.conf`

`zelta.conf` defines jobs for `zelta policy`. It maps sites, hosts, and datasets to one or more backup targets, with options inherited from broad scopes to specific jobs.

Policy files can use `import:` to compose local fragments for sources, targets, and shared rules. Import paths are relative to the file containing the `import:` line.

This file uses YAML-like policy syntax: `KEY: value`. For the configuration overview, see [Environment & Policy Files](/docs/conf/env/). For policy behavior, use `zelta help policy` or `zelta-policy(8)`.
