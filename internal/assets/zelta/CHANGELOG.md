# Changelog

All notable changes to Zelta will be documented in this file.

## [1.1.0] - 2026-01-20

### Known Issues
- **JSON field naming**: Uses mixed camelCase/snake_case conventions. Will align with OpenZFS `zfs list -j` standards in 1.2 or 1.3 after upstream coordination.
- **mawk timestamps**: JSON timestamps require `ZELTA_SYSTIME='date +%s'` when using mawk. gawk and original-awk work without this setting.

### Added
- **Commands**: `zelta revert` for in-place rollbacks via rename and clone.
- **Commands**: `zelta rotate` for divergent version handling, evolved from original `--rotate` flag.
- **Commands**: (Experimental) `zelta prune` identifies snapshots in `zfs destroy` range syntax based on replication state and a sliding window for exclusions.
- **Uninstaller**: Added `uninstall.sh` for clean removal of Zelta installations, including legacy paths from earlier betas.
- **Core**: `zelta-args.awk` added as a separate data-driven argument preprocessor.
- **Core**: `zelta-common.awk` library for centralized string and logging functions.
- **Config**: Data-driven TSV configuration (`zelta-cmds.tsv`, `zelta-cols.tsv`, `zelta-json.tsv`, `zelta-opts.tsv`).
- **Docs**: `zelta.env` expanded with comprehensive inline documentation and examples for all major configuration categories.
- **Docs**: New man pages: `zelta-options(7)`, `zelta-revert(8)`, `zelta-rotate(8)`, `zelta-prune(8)`.
- **Docs**: Added tool to sync man pages with the zelta.space wiki.

### Changed
- **Architecture**: Refactored all core scripts for maintainability and simpler logic.
- **Core**: Improved `bin/zelta` controller with centralized logging and better option handling.
- **Core**: More centralized error handling.
- **Backup**: Rewritten `zelta backup` engine with improved state tracking and resume support.
- **Backup**: Core script renamed from `zelta-replicate.awk` to `zelta-backup.awk`.
- **Backup**: Added granular option overrides via `zfs recv -o` and `-x`.
- **Match**: `zelta match` now calls itself rather than a redundant script.
- **Match**: Output columns are now data-driven with a simpler and clearer 'info' column.
- **Match**: Added exclusion patterns (`-X`, `--exclude`).
- **Policy**: Improved hierarchical scoping and refactored internal job handling with clearer variable naming and function documentation.
- **Rotate**: Better handling of naming.
- **Snapshot**: Operates independently and works with Zelta arguments or an OpenZFS operand.
- **Orchestration**: Zelta is no longer required to be installed on endpoints.
- **Logging**: Better alerts, deprecation system, legacy option handling, and warning messages.
- **Experimental**: Refactored `zelta report` to use newer ZFS features and multiple endpoints.

### Fixed
- Option regressions including legacy overrides and backup depth.
- Better handling of dataset names with spaces and special characters.
- Dataset type detection with environment variables for each (TOP, NEW, FS, VOL, RAW, etc.).
- Improved option hierarchy for `zelta policy`.
- Fixed namespace configuration and repeated targets in `zelta policy`.
- Workaround for GNU Awk 5.2.1 bug.
- Resume token handling and other context-aware ZFS option handling.
- Added `SYSTIME` option for mawk compatibility with JSON timestamps.

### Deprecated
- `zelta endpoint` and other functions have been merged into the core library.
- Dropped unneeded interprocess communication features such as `sync_code` and `-z`.
- Removed "initiator" context, replaced by simple `--pull` (default) and `--push` mechanic.
- Progress pipes (`RECEIVE_PREFIX`) now only work if the local host is involved in replication.

## [1.0.0] - 2024-03-31
- Initial public release for BSDCan 2024.
