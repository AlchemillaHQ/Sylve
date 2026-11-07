# Centralized Policy Example

This example shows a centralized bastion policy composed from small reusable fragments:

- `zelta.yaml` maps source hosts to one or more backup targets.
- `sources/` contains source dataset inventories.
- `targets/` contains target `BACKUP_ROOT` fragments.
- `rules/` contains behavior shared by multiple jobs.

The top-level policy objects (`AWS0`, `NYC1`, `DAL1`, etc.) are operator-defined flow lanes. When `zelta policy` runs with multiple jobs, these lanes are the coarse concurrency units.

## Running The Example

Relative `import:` paths are resolved from the active policy file location. Set `ZELTA_ETC` to the policy directory and `ZELTA_CONFIG` to the exact policy file path:

```sh
export ZELTA_ETC=/path/to/zelta/examples/policy/centralized
export ZELTA_CONFIG="$ZELTA_ETC/zelta.yaml"

zelta policy --dryrun AWS0
zelta policy --dryrun
```

The example host, site, dataset, and client names are sanitized. Replace them with names from your environment before use.

## Fragment Rules

Included fragments are expanded in order before policy parsing. Later options override earlier options, but unspecified options remain in effect. Rule fragments should explicitly set values they depend on, including reset values such as `SNAP_MODE: 0`.
