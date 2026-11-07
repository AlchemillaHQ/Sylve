# JSON Output

Use JSON output when feeding Zelta into monitoring, logs, or reporting tools. This page is operator-focused; field lists and option inheritance live in [zelta-options(7)](/docs/man/zelta-options/).

```sh
zelta backup --json tank/data backup:tank/Backups/data
zelta match --json tank/data backup:tank/Backups/data
zelta policy --json
```

For cron jobs, redirect JSON to a log and import it with your normal telemetry pipeline:

```cron
0 */6 * * * zelta policy --json >> /var/log/zelta-policy.json
```

## Known Limitations

- JSON field names currently use mixed conventions. Aligning with OpenZFS `zfs list -j` naming is planned for a later release; do not hard-code brittle parsers against every key name yet.
- Nested `zelta match` JSON is not a full substitute for human match output today. Prefer human output for interactive diagnosis.
- When using `mawk`, JSON timestamps require `ZELTA_SYSTIME='date +%s'`. `gawk` and original-awk work without this setting.

## Related

- [Simple Backups](/docs/guides/backup/)
- [Policy-Based Automatic Backups](/docs/guides/policy/)
