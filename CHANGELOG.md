# Changelog

All notable changes to Zelta will be documented in this file.

## [1.2.0] - 2026-11-03

### Known Issues
- **Prune grids**: `--prune-grid` terms are evaluated in the order supplied, not sorted automatically. Specify intervals from shortest to longest (e.g. `30x1day,52x1week`), with any unlimited term last, and review candidates before running `zprune`.
- **JSON field naming**: Uses mixed camelCase/snake_case conventions. Will align with OpenZFS `zfs list -j` standards in a later release after upstream coordination.
- **mawk timestamps**: JSON timestamps require `ZELTA_SYSTIME='date +%s'` when using mawk. gawk and original-awk work without this setting.
- **GNU Awk 5.2.1 / 5.3.2**: These releases have a serious memory bug. Zelta warns and prefers original-awk/mawk when available.

### Added
- **Commands**: `zprune` destructive companion for `zelta prune`, with candidate validation, `zfs destroy -nvp` preview, grouped transactions, confirmation prompts, and remote-source destruction.
- **Commands**: `zelta rebase` replication workflow composer for building a new production tree from an upgraded upstream while preserving incremental backup continuity through an existing origin backup.
- **Commands**: `zelta failover` to lock an active source, perform a final backup, sync local ZFS properties, and unlock the promoted target.
- **Commands**: `zelta lock` and `zelta unlock` for ordered dataset-tree readonly, canmount, unmount, and remount workflows.
- **Commands**: `zelta propsync` to replay local ZFS properties from one dataset tree to another while inheriting target-only local overrides.
- **Backup**: `--target-origin` support for backing up existing clones without forcing a full backup when the clone origin already exists on the target side.
- **Backup**: `--bookmark` / `--no-bookmark` / `--bookmark-mode` / `--bookmark-prefix` to create source ZFS bookmarks after confirmed receives (default off).
- **Backup**: `--log-file` / `LOG_FILE` with JSON-aware handling (JSON body to the file; status messages remain on stderr).
- **Clone**: Four-endpoint clone mode which creates a clone, snapshots it, and backs it up using the origin backup as the receive origin.
- **Snapshot**: `--snap-time` and `--snap-size` controls to skip snapshot creation until age or written-size thresholds are reached.
- **Policy**: `import:` support for composing policy files from local fragments, with relative path resolution, recursion protection, and clearer parse errors.
- **Policy**: Centralized policy example tree showing split source, target, and rule fragments.
- **Options**: Global `--include` filtering for dataset and snapshot selection across commands that use the shared argument processor.
- **Backup**: `--include`/`--exclude` filters can now narrow snapshot streams for stepwise replication.
- **Endpoints**: IPv6 hosts in bracket form, e.g. `user@[2001:db8::1]:pool/dataset`.
- **Contrib**: Repo-local Zelta agent skill documentation under `contrib/agent-skills`.
- **Testing**: ShellSpec CI workflow, prune/policy/rebase/encrypted-transition coverage, generated test helpers, golden-pool prune scenarios, and VM test-runner documentation.

### Changed
- **Revert**: Validate the entire tree and selected snapshots before renaming; reject incomplete recursive rewinds. `--force` / `-f` attempts a best-effort revert, continuing unaffected siblings while skipping descendants of unrestored parents. Incomplete results return non-zero without snapshotting or recommending rotate; the entire original tree remains preserved.
- **Prune**: Clarified endpoint roles: acted-on `ENDPOINT` vs optional `--match-endpoint=GUARD` (aliases `--guard-endpoint`, `--prune-guard-endpoint`). Second positional match peer still accepted. `zprune` requires `--match-endpoint` or `--no-prune-guard`.
- **Prune**: Reworked `zelta prune` into a nondestructive candidate planner with explicit retention filters, target guard modes, default 30/30 failsafe behavior, and range-compressed output.
- **Prune**: Added GFS-style `--prune-grid`, reclaim-target `--prune-size`, `--prune-guard=latest|unsynced|none`, `--no-prune-guard`, `--no-ranges`, multi-dataset visual output, and name/policy pruning controls.
- **Prune**: Candidate selection now avoids cloned snapshots and keeps selection separate from destruction; `zelta prune` reports, `zprune` destroys.
- **Prune**: `zelta prune` no longer emits prune-guard warnings (nondestructive); `zprune` still surfaces guard guidance before destruction.
- **Prune**: `--prune-size` multi-dataset notice reports per-dataset reclaim estimates without prescribing `--depth`.
- **zprune**: At normal verbosity, prints destroy commands and a “calculating…” notice before the expensive `zfs destroy -nvp` summary. **-qq** skips dry-run and summary (`-qqf` silent for cron; `-qq` still prompts; `-nqq` candidate-select only).
- **Backup**: Encrypted incremental sends can fall back to decrypted send options when a raw incremental is unavailable because of a broken encryption chain.
- **Backup**: Improved filtered intermediate backup handling when `--include`/`--exclude` patterns narrow the snapshot stream.
- **Failover / lock / unlock / propsync**: More resilient local-only and mount-failure paths; continue final snapshot and replication after readonly is set; multi-operand lock/unlock without loop mode.
- **Match**: Expanded match analysis for encrypted targets, IV set comparison, written-size reporting, multiple operands, and dynamic report output.
- **Match**: Usage output now documents shared filter options including `--depth`, `--exclude`, and `--include`.
- **JSON**: Drop null fields from JSON output, sanitize control characters (including CR), and terminate JSON output with a newline for cleaner piping.
- **Install**: One-shot installer now downloads GitHub branch archives instead of requiring `git`, detects user install paths, reports up-to-date installs, and prints the installed version.
- **Docs**: Updated backup, clone, match, policy, prune, snapshot, revert, rotate, options, failover, lock/unlock, and `zprune` man page sources for the new workflows; generated man pages are organized under `doc/man7` and `doc/man8`.
- **Docs**: Added in-repository wiki source documents for the active Zelta wiki, including install, overview, start guide, FAQ, backup, policy, sync, recovery, JSON, SSH, ZFS delegation, and environment configuration.

### Fixed
- **Revert**: Check whole-tree listing completion explicitly, rejecting partial output from failed commands even when Original Awk discards pipe exit status. The general listing-truncation limitation remains documented in **zelta(8)**.
- **Prune**: Fixed the ignored `--prune-name` filter.
- **Exclude**: Fixed `--exclude` behavior with snapshot range compression and corrected prune usage from `-x` to `-X`.
- **Prune**: Fixed explicit prune selector handling, safe preview output, grouped destroy previews, recursion in `zprune`, and snapshot space descriptions.
- **Prune**: Fixed `zprune --dryrun` so preview mode does not leak into prune candidate selection.
- **Prune**: `zprune` now bootstraps through `zelta ipc-env`, preserving the same environment/default resolution as normal Zelta commands.
- **Policy**: Fixed config install location, import parsing errors, reporting regressions, and repeated target handling.
- **Policy**: Fixed a regression where `zelta policy` incorrectly invoked `zelta ipc-run backup` instead of `backup`.
- **Backup**: Improved raw-encrypted transition handling, resume/error JSON output, backup validation, and snapshot count checks on the associated server.
- **Core**: Warning and preferred-awk fallback for GNU Awk 5.2.1/5.3.2 memory bug.
- **Testing**: Cleaned sandbox cleanup, spec output, generated divergent/revert/clone/prune/policy tests, and local ShellSpec execution.

## [1.1.0] - 2026-01-20

### Known Issues
- **JSON field naming**: Uses mixed camelCase/snake_case conventions. Will align with OpenZFS `zfs list -j` standards in 1.2 or 1.3 after upstream coordination.
- **mawk timestamps**: JSON timestamps require `ZELTA_SYSTIME='date +%s'` when using mawk. gawk and original-awk work without this setting.

### Added
- **Commands**: `zelta revert` for in-place rollbacks via rename and clone.
- **Commands**: `zelta rotate` for divergent version handling, evolved from original `--rotate` flag.
- **Commands**: (Experimental) `zelta prune` identifies snapshots in `zfs destroy` range syntax based on replication state and a sliding window for exclusions.
- **Installer**: (Experimental) Added a one-liner/pipe to shell installer option.
- **Uninstaller**: Added `uninstall.sh` for clean removal of Zelta installations, including legacy paths from earlier betas.
- **Core**: `zelta-args.awk` added as a separate data-driven argument preprocessor.
- **Core**: `zelta-common.awk` library for centralized string and logging functions.
- **Config**: Data-driven TSV configuration (`zelta-cmds.tsv`, `zelta-cols.tsv`, `zelta-json.tsv`, `zelta-opts.tsv`).
- **Docs**: `zelta.env` expanded with comprehensive inline documentation and examples for all major configuration categories.
- **Docs**: New man pages: `zelta-options(7)`, `zelta-revert(8)`, `zelta-rotate(8)`, `zelta-prune(8)`.
- **Docs**: Added tool to sync man pages with the zelta.space wiki.
- **Testing**: Added new advanced Shellspec-based testing suite.

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
- Workaround for GNU Awk 5.2.1/5.3.2 memory bug.
- Resume token handling and other context-aware ZFS option handling.
- Added `SYSTIME` option for mawk compatibility with JSON timestamps.

### Deprecated
- `zelta endpoint` and other functions have been merged into the core library.
- Dropped unneeded interprocess communication features such as `sync_code` and `-z`.
- Removed "initiator" context, replaced by simple `--pull` (default) and `--push` mechanic.
- Progress pipes (`RECEIVE_PREFIX`) now only work if the local host is involved in replication.

## [1.0.0] - 2024-03-31
- Initial public release for BSDCan 2024.
