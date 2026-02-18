#!/usr/bin/env awk -f
#
# zelta-match.awk
#
# Describes the relationship between two trees of ZFS datasets. Run a "zfs list"
# command on the source endpoint piping to itself for concurrency, run a second
# target endpoint zfs list via 'getline', and compare the results.
#
# Global: Settings and global telemetry
# Source: The source endpoint
# Target: The target endpoint
# Row: 'zfs list' output
# Dataset: A list of datasets
# Snap: A list of snapshots and bookmarks for each dataset
# NumSnaps: The ordered reference for Snap for each Dataset


## Usage
########

function usage(message,		_counter, _c, _key) {
	if (Opt["VERB"]== "prune")
		usage_prune(message)
	STDERR = "/dev/stderr"
	usage_table = "\t%-13s%s\n"
	printf (message ? message "\n" : "") "usage:"                    > STDERR
	print "\tmatch [-Hp] [-d max] [-o field[,...]] SOURCE TARGET\n"  > STDERR
	print "The following fields are supported:\n"                    > STDERR
	printf usage_table"\n", "PROPERTY", "VALUES"                     > STDERR
	for(_counter in ColInfo) {
		_key = ColList[++_c]
		if (ColWarn[_key])
			continue
		printf usage_table, _key, ColInfo[_key] > STDERR
	}
	print "\nSizes are specified in bytes with standard units such as K, M, G, etc.\n"         > STDERR
	print "SOURCE and TARGET endpoints are in the form: [user@host:]pool[/dataset/][@snap]\n"  > STDERR
	print "For complete documentation:  zelta help [<topic>]"                                  > STDERR
	print "                             https://zelta.space"                                   > STDERR
	stop(1)
}

function usage_prune(message) {
	STDERR = "/dev/stderr"
	printf (message ? message "\n" : "") "usage:"                                                       > STDERR
	print "\tprune [--keep-snap-num=N] [--keep-snap-days=N] [-X pattern] SOURCE TARGET\n"               > STDERR
	print "Identifies snapshots on SOURCE that exist on TARGET.\n"                                       > STDERR
	print "Options:"                                                                                    > STDERR
	print "\t--keep-snap-num=N    Minimum number of snapshots to keep after match (default: 10)"        > STDERR
	print "\t--keep-snap-days=N   Minimum age in days for snapshot deletion (default: 90)"              > STDERR
	print "\t--no-ranges          Disable range compression (output individual snapshots)"              > STDERR
	print "\t-X pattern           Exclude datasets matching pattern\n"                                  > STDERR
	print "Only snapshots older than the common match point and replicated to TARGET are considered."   > STDERR
	print "Output shows snapshot names (one per line) that are safe to prune.\n"                        > STDERR
	print "For complete documentation:  zelta help prune"                                               > STDERR
	print "                             https://zelta.space"                                            > STDERR
	stop(1)
}


## Command Building
###################

# Default to 'zfs list ... -o written', but implicitly avoid since it's slow
function add_written() {
	if (Opt["LIST_WRITTEN"] && Opt["PROPLIST"]) {
		if (Opt["PARSABLE"] && (Opt["PROPLIST"] !~ /(all|written|size)/))
			return ""
	}
	return Opt["LIST_WRITTEN"] ? ",written,creation" : ""
}

# TO-DO: Add this feature to build_command()
function wrap_time_cmd(cmd,		_cmd_part, _p) {
	_cmd_part[++_p] = Opt["SH_COMMAND_PREFIX"]
	_cmd_part[++_p] = Opt["TIME_COMMAND"]
	_cmd_part[++_p] = cmd
	_cmd_part[++_p] = Opt["SH_COMMAND_SUFFIX"]
	cmd             = arr_join(_cmd_part)
	return cmd
}

# Generate the 'zfs list' command using build_command()
function zfs_list_cmd(endpoint,		_ep, _ds, _remote, _cmd) {
	if (!endpoint["DS"]) return
	_ep			= endpoint["ID"]
	_ds			= endpoint["DS"]
	_remote			= endpoint["REMOTE"]
	_cmd_arr["props"]	= "name,guid" add_written()
	_cmd_arr["remote"]	= get_remote_cmd(endpoint)
	_cmd_arr["ds"]		= rq(_remote, _ds)
	if (Opt["DEPTH"])
		_cmd_arr["flags"] = "-d" Opt["DEPTH"]
	_cmd			= build_command("LIST", _cmd_arr)
	if (Opt["DRYRUN"]) _cmd	= report(LOG_NOTICE, "+ " _cmd)
	if (Opt["CHECK_TIME"]) _cmd	= wrap_time_cmd(_cmd)
	_cmd			= str_add(_cmd, CAPTURE_OUTPUT)
	return _cmd
}

#  Send the Source 'zfs list' to a second process for concurrency
#  (Our biggest bottleneck is waiting for the lists to complete and buffer)
function pipe_zfs_list_source(		_match_cmd, _src_list_cmd) {
	_match_cmd	= "ZELTA_MATCH_PIPE=yes zelta ipc-run " Opt["VERB"]
	_src_list_cmd	= zfs_list_cmd(Source)
	if (Opt["DRYRUN"]) {
		zfs_list_cmd(Target)
		stop(0)
	}
	report(LOG_DEBUG, "`"_match_cmd"`")
	report(LOG_INFO, "listing source: " Source["ID"])
	report(LOG_DEBUG, "`" _src_list_cmd "`")

	# The blank line piped below allows the target awk stream to run its
	# BEGIN block without waiting for first line of 'zfs list' output.
	print "" | _match_cmd
	while (_src_list_cmd | getline) print | _match_cmd
	close(_src_list_cmd)
	close(_match_cmd)
}

# After the triggering the pipe phase, fire the Target 'zfs list' and parse the rows
function run_zfs_list_target(		_src_list_cmd) {
	if ((Source["ID"] == Target["ID"])) {
		report(LOG_WARNING, "identical source and target; skipping 'zfs list' for target")
		return
	}
	# Load target snapshots
	_tgt_list_cmd = zfs_list_cmd(Target)
	report(LOG_INFO, "listing target: " Target["ID"])
	report(LOG_DEBUG, "`" _tgt_list_cmd "`")
	_tgt_list_cmd = str_add(_tgt_list_cmd, CAPTURE_OUTPUT)
	while  (_tgt_list_cmd | getline)
		load_zfs_list_row(Target)
	close(_tgt_list_cmd)
}


## Row parsing
##############

function depth_ok(ds_suffix,	_depth, _tmp_arr) {
	_depth	= split(ds_suffix, _tmp_arr, "/")
	if (Opt["DEPTH"] && (_depth > Opt["DEPTH"]))
		return 0
	else
		return 1
}

# Identify if the row refers to a dataset, snapshot, or bookmark
function object_type(symbol) {
	if (symbol == "")	return IS_DATASET
	else if (symbol == "@")	return IS_SNAPSHOT
	else if (symbol == "#")	return IS_BOOKMARK
	else {
		report(LOG_WARNING, "unexpected row: " symbol)
		return IS_UNKNOWN
	}
}

# Load each row into memory
function process_row(ep,		_name, _guid, _written, _name_suffix, _ds_suffix, _savepoint,
		     			_type, _ep_id, _ds_id, _ds_snap, _row_id, _tmp_arr, _num_snaps) {
	# Read the row data
	_name      = $1
	_guid      = $2
	_written   = $3
	_creation  = $4

	# Get the relative dataset suffix and then split to dataset and snapshot/bookmark name
	_name_suffix		= substr(_name, ep["ds_length"])
	match(_name_suffix,/[@#]/)
	if (RSTART) {
		_ds_suffix		= substr(_name_suffix, 1, RSTART - 1)
		_savepoint		= substr(_name_suffix, RSTART)
		_type			= substr(_savepoint, 1, 1)
	} else 	_ds_suffix		= _name_suffix
	if (!depth_ok(_ds_suffix))
		return

	_ep_id			= ep["ID"]
	_ds_id			= _ep_id S _ds_suffix S ""
	_row_id			= _ep_id S _ds_suffix S _savepoint
	_type			= object_type(_type)

	# Check for exclusion
	if (_type == IS_DATASET) {
		if (is_ds_excluded(_name))
			return
		if (regex_loop(_ds_suffix, ExcludeDSPattern, NumExcludeDS))
			return
	}
	if ((_type == IS_SNAPSHOT) && (_ep_id == Source["ID"]))
		if (regex_loop(_savepoint, ExcludeSnapPattern, NumExcludeSnap))
			return

	Row[_row_id, "exists"]     = 1
	Row[_row_id, "guid"]       = _guid
	Row[_row_id, "written"]    = _written
	Row[_row_id, "creation"]   = _creation
	Row[_row_id, "name"]       = _name
	Row[_row_id, "type"]       = _type
	Row[_row_id, "ds_suffix"]  = _ds_suffix

	# Snapshots will be used for match GUID over bookmarks
	if (!Guid[_ds_id, _guid] || (_type == IS_SNAPSHOT))
		Guid[_ds_id, _guid] = _row_id

	# Dataset
	if (_type == IS_DATASET) {
		# Note: 'zfs list -S createtxg' gives us a reverse view of datasets
		_num_ds				= ++ep["num_ds"]
		Dataset[_ep_id, _num_ds]	= _row_id
		Global["written"]		+= $3
	# Snapshot or bookmark
	} else if ((_type == IS_SNAPSHOT) || (_type == IS_BOOKMARK)) {
		_num_snaps			= ++NumSnaps[_ds_id]
		Snap[_ds_id, _num_snaps]	= _row_id
		Row[_row_id, "savepoint"]	= _savepoint
	}
}

# Check for exceptions or time(1) output, or process the row
function load_zfs_list_row(ep,		_time_arr) {
	IGNORE_ZFS_LIST_OUTPUT="(sys|user)[ \t]+[0-9]|dataset does not exist"
	if ($0 ~ IGNORE_ZFS_LIST_OUTPUT) return
	if (/^real[ \t]+[0-9]/) {
		split($0, _time_arr, /[ \t]+/)
		ep["list_time"] += _time_arr[2]
	}
	else if ($2 ~ /^[0-9]+$/) {
		process_row(ep)
	} else {
		report(LOG_WARNING, "stream output unexpected: "$0)
	}
}


## Identifying Replica Relationships
####################################


# Exclude patterns
function load_exclude_patterns(    _i, _n, _pat_arr, _pat, _g2r) {
	if (!Opt["EXCLUDE"]) return

	_n = split(Opt["EXCLUDE"], _pat_arr, ",")
	for (_i = 1; _i <= _n; _i++) {
		_pat = _pat_arr[_i]

		if (_pat ~ /^\/|^\*.*\//)
			ExcludeDSPattern[++NumExcludeDS] = glob_to_regex(_pat, "(/.*)?")
		else if (_pat ~ /^@/)
			ExcludeSnapPattern[++NumExcludeSnap] = glob_to_regex(_pat)
		else if (_pat ~ /[\*\?]/)
			report(LOG_WARNING, "invalid exclusion pattern '"_pat"' must start with '@' or include '/'")
		else
			ExcludeDS[_pat] = 1
	}
}

function regex_loop(string, pat_arr,        n, _i) {
	for (_i = 1; _i <= n; _i++)
		if (string ~ pat_arr[_i])
			return 1
}

function is_ds_excluded(string,             _i, _pat) {
	if (string in ExcludeDS)
		return 1
	# Check for descendents
	for (_i in ExcludeDS) {
		if (!_i) continue
		_pat = _i "/"
		if (index(string, _pat) == 1)
			return 1
	}
}

# Load DSPair keys for summary output
function create_ds_pair(row_id,		_row_arr, _ds_suffix, _src_ds, _tgt_ds) {
	split(row_id, _row_arr, S)
	_ds_suffix		= _row_arr[2]
	_src_ds			= Source["ID"] S _ds_suffix S ""
	_tgt_ds			= Target["ID"] S _ds_suffix S ""
	DSPairList[++NumDSPair]	= _ds_suffix

	# DSPair contains all output columns, so some Row[] fields must be copied
	DSPair[_ds_suffix, "ds_suffix"]		= _ds_suffix
	DSPair[_ds_suffix, "src_name"]		= Row[_src_ds, "name"]
	DSPair[_ds_suffix, "tgt_name"]		= Row[_tgt_ds, "name"]
	DSPair[_ds_suffix, "src_written"]	= Row[_src_ds, "written"]
	DSPair[_ds_suffix, "tgt_written"]	= Row[_tgt_ds, "written"]
	DSPair[_ds_suffix, "src_snaps"]		= NumSnaps[_src_ds]
	DSPair[_ds_suffix, "tgt_snaps"]		= NumSnaps[_tgt_ds]
	DSPair[_ds_suffix, "tgt_written"]	= Row[_tgt_ds, "written"]
	DSPair[_ds_suffix, "src_first"]		= Row[Snap[_src_ds,NumSnaps[_src_ds]], "savepoint"]
	DSPair[_ds_suffix, "tgt_first"]		= Row[Snap[_tgt_ds,NumSnaps[_tgt_ds]], "savepoint"]
	DSPair[_ds_suffix, "src_last"]		= Row[Snap[_src_ds,1], "savepoint"]
	DSPair[_ds_suffix, "tgt_last"]		= Row[Snap[_tgt_ds,1], "savepoint"]
}

function compare_datasets(src_ds_id,		_ds_suffix, _row_arr, _tgt_ds_id) {
	split(src_ds_id, _row_arr, S)
	_ds_suffix = _row_arr[2]
	_tgt_ds_id = Target["ID"] S _ds_suffix S ""
	if (Row[_tgt_ds_id, "exists"]) {
		DSPair[_ds_suffix, "status"] = PAIR_EXISTS
		return 1
	} else {
		DSPair[_ds_suffix, "status"] = PAIR_SRC_ONLY
		return 0
	}
}

# TO-DO: Add user-defined filters
function validate_match(src_row, tgt_row, ds_suffix, savepoint, snap_idx) {
	# Exclude if the target isn't a snapshot
	if (Row[tgt_row, "type"] != IS_SNAPSHOT)
		return
	if (!DSPair[ds_suffix, "num_matches"]++) {
		# TO-DO: Validate by filter
		DSPair[ds_suffix, "match"] = savepoint
		# Record match index for pruning
		DSPair[ds_suffix, "match_idx"] = snap_idx
	}
}

# Step through snapshots for counters and to find common snapshots
function compare_snapshots(src_row, idx,	_src_row_arr, _ds_suffix, _savepoint, _src_guid, _tgt_ds_id, _tgt_match) {
	# Identify a match candidate by GUID
	split(src_row, _src_row_arr, S)
	_ds_suffix	= _src_row_arr[2]
	_savepoint	= _src_row_arr[3]
	_src_guid	= Row[src_row, "guid"]
	_tgt_ds_id	= Target["ID"] S _ds_suffix S ""
	_tgt_match	= Guid[_tgt_ds_id, _src_guid]
	if (_tgt_match)
		validate_match(src_row, _tgt_match, _ds_suffix, _savepoint, idx)
	else {
		if (!DSPair[_ds_suffix, "match"]) {
			DSPair[_ds_suffix, "src_next"] = _savepoint
			DSPair[_ds_suffix, "xfer_num"]++
			DSPair[_ds_suffix, "xfer_size"] += Row[src_row, "written"]
		}
	}
}

function review_target_datasets(tgt_id,		_tgt_arr, _ds_suffix, _num_snaps, _tgt_row, _savepoint,
						_s, _row_arr, _guid, _src_ds_id,_match, _match_found) {
	split(tgt_id, _tgt_arr, S)
	_ds_suffix = _tgt_arr[2]
	_num_snaps = NumSnaps[tgt_id]
	if (!DSPair[_ds_suffix,"status"]) {
		create_ds_pair(tgt_id)
		DSPair[_ds_suffix,"tgt_snaps"] = _num_snaps
		DSPair[_ds_suffix,"status"] = PAIR_TGT_ONLY
	}
	for (_s = 1; _s <= _num_snaps; _s++) {
		_tgt_row = Snap[tgt_id,_s]
		split(_tgt_row, _row_arr, S)
		_savepoint	= _row_arr[3]
		_guid		= Row[_tgt_row, "guid"]
		_src_ds_id	= Source["ID"] S _ds_suffix S ""
		_match		= Guid[_src_ds_id, _guid]
		if (_match)
			_match_found = 1
		if (!_match_found && (Row[_tgt_row, "type"] == IS_SNAPSHOT)) {
			DSPair[_ds_suffix, "num_blocked"]++
			DSPair[_ds_suffix, "tgt_next"] = _savepoint
		}
	}
}

function process_datasets(		_src_id, _tgt_id, _num_src_ds, _num_tgt_ds, _d, _s,
			  		_src_ds_id, _match, _num_snaps, _tgt_ds_id) {
	_src_id		= Source["ID"]
	_tgt_id		= Target["ID"]
	_num_src_ds	= Source["num_ds"]
	_num_tgt_ds	= Target["num_ds"]

	# Step through source objects
	for (_d = 1; _d <= _num_src_ds; _d++) {
		_src_ds_id = Dataset[_src_id, _d]
		create_ds_pair(_src_ds_id)
		_match = compare_datasets(_src_ds_id)
		_num_snaps = NumSnaps[_src_ds_id]
		for (_s = 1; _s <= _num_snaps; _s++)
			compare_snapshots(Snap[_src_ds_id,_s], _s)
	}

	# Step through target objects
	for (_d = 1; _d <= _num_tgt_ds; _d++) {
		_tgt_ds_id = Dataset[_tgt_id, _d]
		review_target_datasets(Dataset[_tgt_id, _d])
	}
	arr_sort(DSPairList, NumDSPair)
}


## Postprocessing
#################

# Report up-to-date, syncable, blocked sync, or no source
function get_info(	_d, _ds_suffix, _src_ds, _tgt_ds, _info, _blocked, _s) {
	for (_d = 1; _d <= NumDSPair; _d++) {
		_ds_suffix          = DSPairList[_d]
		_src_ds             = Source["ID"] S _ds_suffix S ""
		_tgt_ds             = Target["ID"] S _ds_suffix S ""

		_blocked = ""
		delete _info


		if (DSPair[_ds_suffix, "status"] == PAIR_TGT_ONLY) {
			DSPair[_ds_suffix, "info"] = "no source (target only)"
			Global["blocked_count"]++
			continue
		}

		if (DSPair[_ds_suffix, "status"] == PAIR_SRC_ONLY) {
			DSPair[_ds_suffix, "info"] = "syncable (full)"
			Global["syncable_count"]++
			continue
		}

		# Identify blocked syncs to start
		if (!NumSnaps[_tgt_ds] && NumSnaps[_src_ds])
			_blocked = "no target snapshots"
		else if (DSPair[_ds_suffix, "match"] != DSPair[_ds_suffix, "tgt_last"])
			_blocked = "target diverged"
		else if (Row[_tgt_ds, "written"])
			_blocked = "target is written"
		# List all reasons the sync is blocked
		if (_blocked) {
			DSPair[_ds_suffix, "info"] = "blocked sync: " _blocked
			Global["blocked_count"]++
			continue
		}


		if (DSPair[_ds_suffix, "match"] == DSPair[_ds_suffix, "src_last"]) {
			DSPair[_ds_suffix, "info"] = "up-to-date"
			Global["up_to_date_count"]++
		}
		else if (DSPair[_ds_suffix, "match"] == DSPair[_ds_suffix, "tgt_last"]) {
			DSPair[_ds_suffix, "info"] = "syncable (incremental)"
			Global["syncable_count"]++
		}
		else {
			report(LOG_WARNING, Row[_src_ds, "name"] ": unexpected state")
			Global["match_error"]++
		}
	}

	if (Global["up_to_date_count"])
		_sum_arr[++_s] = Global["up_to_date_count"] " up-to-date"
	if (Global["syncable_count"])
		_sum_arr[++_s] = Global["syncable_count"] " syncable"
	if (Global["blocked_count"])
		_sum_arr[++_s] = Global["blocked_count"] " blocked"
	Global["summary"] = arr_join(_sum_arr, ", ")
}

# Check if target has a snapshot with the same name (not just same GUID)
function target_has_snap_name(tgt_ds_id, savepoint,		_num_snaps, _s, _tgt_row, _tgt_savepoint) {
	_num_snaps = NumSnaps[tgt_ds_id]
	for (_s = 1; _s <= _num_snaps; _s++) {
		_tgt_row = Snap[tgt_ds_id, _s]
		_tgt_savepoint = Row[_tgt_row, "savepoint"]
		if (_tgt_savepoint == savepoint)
			return 1
	}
	return 0
}

# Analyze snapshots for pruning eligibility
# Only outputs snapshots that ARE replicated to target (safe to prune)
# Requires both GUID match AND name match for deletion
function analyze_prune_candidates(		_d, _ds_suffix, _src_ds_id, _tgt_ds_id, _num_snaps,
						_s, _src_row, _savepoint, _guid, _creation,
						_match_idx, _snap_seconds, _min_age, _keep_after_match,
						_has_name_match) {

	# SECONDS overrides DAYS if set
	if (Opt["KEEP_SNAP_SECONDS"])
		_snap_seconds = Opt["KEEP_SNAP_SECONDS"]
	else
		_snap_seconds = Opt["KEEP_SNAP_DAYS"] * 86400
	_min_age = sys_time() - _snap_seconds
	_keep_after_match = Opt["KEEP_SNAP_NUM"]

	for (_d = 1; _d <= NumDSPair; _d++) {
		_ds_suffix = DSPairList[_d]
		_src_ds_id = Source["ID"] S _ds_suffix S ""
		_tgt_ds_id = Target["ID"] S _ds_suffix S ""
		_num_snaps = NumSnaps[_src_ds_id]

		_match_idx = DSPair[_ds_suffix, "match_idx"]
		if (!_match_idx) continue

		# Analyze snapshots older than match (higher index = older)
		for (_s = _match_idx + 1; _s <= _num_snaps; _s++) {
			_src_row = Snap[_src_ds_id, _s]
			_savepoint = Row[_src_row, "savepoint"]
			_guid = Row[_src_row, "guid"]
			_creation = Row[_src_row, "creation"]

			# Only consider snapshots (not bookmarks)
			if (Row[_src_row, "type"] != IS_SNAPSHOT) continue

			# Only prune if replicated to target (GUID exists on target)
			if (!Guid[_tgt_ds_id, _guid]) continue

			# Check if target has snapshot with same name
			_has_name_match = target_has_snap_name(_tgt_ds_id, _savepoint)

			# Keep if within retention window OR if name doesn't match on target
			if (((_s - _match_idx) <= _keep_after_match) || (_min_age && (_creation >= _min_age)) || !_has_name_match) {
				KeptSnap[_src_ds_id, ++NumKeptSnap[_src_ds_id]] = _savepoint
				KeptSnapIdx[_src_ds_id, NumKeptSnap[_src_ds_id]] = _s
				continue
			}

			# Mark as prune candidate (store with index for range compression)
			PruneSnap[_src_ds_id, ++PruneSnapNum[_src_ds_id]] = _savepoint
			PruneSnapIdx[_src_ds_id, PruneSnapNum[_src_ds_id]] = _s
		}
	}
}

# Compress contiguous snapshots into ranges
# Input: snap_arr[ds_id, n] = "@snap1", "@snap2", "@snap3"
#        snap_idx_arr[ds_id, n] = index positions
#        snap_num_arr[ds_id] = count
# Output: range_arr[ds_id, n] = "@snap1%snap3" (if contiguous)
#         range_num_arr[ds_id] = count of ranges
function compress_snapshot_ranges(snap_arr, snap_idx_arr, snap_num_arr, range_arr, range_num_arr,
					_d, _ds_suffix, _src_ds_id, _p, _range_start, _range_end,
					_range_start_idx, _prev_idx, _curr_idx, _num_ranges) {

	for (_d = 1; _d <= NumDSPair; _d++) {
		_ds_suffix = DSPairList[_d]
		_src_ds_id = Source["ID"] S _ds_suffix S ""

		if (!snap_num_arr[_src_ds_id]) continue

		_range_start = ""
		_range_end = ""
		_range_start_idx = 0
		_prev_idx = 0
		_num_ranges = 0

		# Iterate through snapshots (oldest to newest, reverse order)
		for (_p = snap_num_arr[_src_ds_id]; _p >= 1; _p--) {
			_curr_idx = snap_idx_arr[_src_ds_id, _p]

			# Start a new range
			if (!_range_start) {
				_range_start = snap_arr[_src_ds_id, _p]
				_range_start_idx = _curr_idx
				_range_end = _range_start
				_prev_idx = _curr_idx
				continue
			}

			# Check if contiguous (indices differ by 1)
			if (_curr_idx == _prev_idx - 1) {
				# Extend the range
				_range_end = snap_arr[_src_ds_id, _p]
				_prev_idx = _curr_idx
			} else {
				# Non-contiguous: save current range and start new one
				if (_range_start == _range_end) {
					# Single snapshot
					range_arr[_src_ds_id, ++_num_ranges] = _range_start
				} else {
					# Range of snapshots
					range_arr[_src_ds_id, ++_num_ranges] = _range_start "%" substr(_range_end, 2)
				}
				_range_start = snap_arr[_src_ds_id, _p]
				_range_start_idx = _curr_idx
				_range_end = _range_start
				_prev_idx = _curr_idx
			}
		}

		# Save final range
		if (_range_start) {
			if (_range_start == _range_end) {
				range_arr[_src_ds_id, ++_num_ranges] = _range_start
			} else {
				range_arr[_src_ds_id, ++_num_ranges] = _range_start "%" substr(_range_end, 2)
			}
		}

		range_num_arr[_src_ds_id] = _num_ranges
	}
}

# Build kept snapshots string with range compression
function build_kept_string(		_d, _ds_suffix, _src_ds_id, _k, _kept_str, _base_name) {
	# Compress kept ranges
	compress_snapshot_ranges(KeptSnap, KeptSnapIdx, NumKeptSnap, KeptRange, KeptRangeNum)

	for (_d = 1; _d <= NumDSPair; _d++) {
		_ds_suffix = DSPairList[_d]
		_src_ds_id = Source["ID"] S _ds_suffix S ""
		_base_name = Source["DS"] _ds_suffix

		if (KeptRangeNum[_src_ds_id]) {
			for (_k = KeptRangeNum[_src_ds_id]; _k >= 1; _k--) {
				_kept_str = str_add(_kept_str, _base_name KeptRange[_src_ds_id, _k], ",")
			}
		}
	}
	return _kept_str
}

# Output prune candidates - just snapshot names, one per line
function output_prune(		_d, _ds_suffix, _src_ds_id, _p, _range, _base_name, _kept_str) {
	if (!NumDSPair) {
		report(LOG_ERROR, "datasets inaccessible or do not exist")
		return
	}

	# Compress ranges unless disabled
	if (!Opt["NO_RANGES"])
		compress_snapshot_ranges(PruneSnap, PruneSnapIdx, PruneSnapNum, PruneRange, PruneRangeNum)

	# Build and output kept snapshots with range compression
	_kept_str = build_kept_string()
	if (_kept_str)
		report(LOG_INFO, "keeping: " _kept_str)

	# Output one snapshot or range per line (oldest first)
	for (_d = 1; _d <= NumDSPair; _d++) {
		_ds_suffix = DSPairList[_d]
		_src_ds_id = Source["ID"] S _ds_suffix S ""
		_base_name = Row[_src_ds_id, "name"]

		if (Opt["NO_RANGES"]) {
			# Output individual snapshots
			if (PruneSnapNum[_src_ds_id]) {
				for (_p = PruneSnapNum[_src_ds_id]; _p >= 1; _p--)
					report(LOG_NOTICE, _base_name PruneSnap[_src_ds_id, _p])
			}
		} else {
			# Output compressed ranges
			if (PruneRangeNum[_src_ds_id]) {
				for (_p = PruneRangeNum[_src_ds_id]; _p >= 1; _p--)
					report(LOG_NOTICE, _base_name PruneRange[_src_ds_id, _p])
			}
		}
	}
}

## Output
#########

# Load the column data
function load_columns(		_tsv, _key, _opt_list, _opt, _idx, _c, _default_proplist, _proplist, _p) {
	_tsv = Opt["SHARE"]"/zelta-cols.tsv"
	FS="\t"
	while ((getline<_tsv)>0) {
		if (/^#/) continue
		_key = $1
		split($2, _opt_list, ",")
		for (_idx in _opt_list) {
			_opt          = _opt_list[_idx]
			ColOpt[_opt]  = str_add(ColOpt[_opt], _key, S)
		}
		ColType[_key]       = $3
		if ((ColType[_key] == "num") || (ColType[_key] == "bytes"))
			ColNum[_key]    = 1
		if (ColType[_key] == "bytes")
			ColBytes[_key]  = 1
		ColInfo[_key]       = $4
		ColWarn[_key]       = $5

		ColList[++_c]       = _key
	}
	close(_tsv)

	_default_proplist = "dssuffix,match,last,info"
	_proplist = Opt["PROPLIST"] ? Opt["PROPLIST"] : _default_proplist
	if (_proplist == "all")
		_proplist = arr_join(ColList, ",")
	gsub(/_/, "", _proplist)
	_nt = split(_proplist, _prop_tmp, ",")
	for (_t = 1; _t <= _nt; _t++) {
		_prop_opts = ColOpt[_prop_tmp[_t]]
		if (!_prop_opts)
			usage("bad property list: invalid property '"  _prop_tmp[_t] "'")
		else {
			_np = split(_prop_opts, _prop_opt_arr, S)
			for (_p = 1; _p <= _np; _p++) {
				PropList[++NumProps] = _prop_opt_arr[_p]
			}
		}
	}
}

# Load override values for DSPair
function get_column_value(ds_suffix, key,	_val) {
	_val = DSPair[ds_suffix, key]
	# In scripting mode, just make sure numbers are formatted correctly
	if (Opt["SCRIPTING_MODE"]) {
		if (!_val && (key in ColNum))
			return "0"
		return _val
	}

	# Normal output shows appropriate placeholders for null values
	if (!_val) {
		if (key == "ds_suffix")
			return "[" Source["LEAF"] "]"
		else if (key in ColBytes)
			return "0B"
		else if (key in ColInt)
			return "0"
		else
			return "-"
	} else if (key in ColBytes)
		return h_num(_val)
	return DSPair[ds_suffix, key]
}

function get_cell(c, key, val,		_cell) {
	if (Opt["SCRIPTING_MODE"]) {
		if (!val && (ColType[key] == "int"))
			val = "0"
		_cell = (c == 1) ? val : "\t" val
	} else {
		_cell = (c == 1) ? "" : "  "
		_cell = _cell sprintf("%-*s", ColLen[key], val)
	}
	return _cell
}

# Adjust visuals for human output (without 'SCRIPTING_MODE')
function print_header(		_c, _key, _r, _ds_suffix, _len, _line) {
	if (Opt["SCRIPTING_MODE"]) return
	# Figure out column widths of column names and DSPair[] values for pretty printing
	for (_c = 1; _c <= NumProps; _c++) {
		#_key = ColOpt[PropList[_c]]
		_key = PropList[_c]
		ColLen[_key] = length(_key)
		for (_r = 1; _r <= NumDSPair; _r++) {
			_ds_suffix = DSPairList[_r]
			DSPair[_ds_suffix, _key] = get_column_value(_ds_suffix, _key)
			_key_len = length(DSPair[_ds_suffix, _key])
			if (_key_len > ColLen[_key])
				ColLen[_key] = _key_len
		}
		_line = _line get_cell(_c, _key, toupper(_key))
	}
	report(LOG_NOTICE, _line)
}

# Print the output summary
function summary(	_r, _line, _ds_suffix, _c, _key, _val, _cell) {
	if (!NumDSPair) {
		report(LOG_ERROR, "datasets inaccessible or do not exist")
		return
	}
	print_header()
	for (_r = 1; _r <= NumDSPair; _r++) {
		_line = ""
		_ds_suffix = DSPairList[_r]
		for (_c = 1; _c <= NumProps; _c++) {
			_cell = ""
			#_key = ColOpt[PropList[_c]]
			_key = PropList[_c]
			_val = DSPair[_ds_suffix, _key]
			_line = _line get_cell(_c, _key, _val)
		}
		report(LOG_NOTICE, _line)
	}
	if (!Opt["SCRIPTING_MODE"]) {
		report(LOG_NOTICE, Global["summary"])
		if ((NumDSPair > 1) && (Global["summary"] ~ /,/))
			report(LOG_NOTICE, NumDSPair " total datasets compared")
	}
	if (Opt["CHECK_TIME"]) {
		if (Source["list_time"])
			report(LOG_NOTICE, "SOURCE_LIST_TIME:\t" Source["list_time"])
		if (Target["list_time"])
			report(LOG_NOTICE, "TARGET_LIST_TIME:\t" Target["list_time"])
	}
}

## Main Workflow Rules
######################

# Constant setup, validation, and fire concurrent 'zfs list' commands
BEGIN {
	# Row types
	IS_UNKNOWN     = 0
	IS_DATASET     = 1
	IS_SNAPSHOT    = 2
	IS_BOOKMARK    = 3

	# DSPair types
	PAIR_UNKNOWN   = 0
	PAIR_EXISTS    = 1
	PAIR_SRC_ONLY  = 2
	PAIR_TGT_ONLY  = 3

	S              = SUBSEP
	FS             = "\t"
	OFS            = "\t"

	load_endpoint(Operands[1], Source)
	load_endpoint(Operands[2], Target)
	load_columns()
	if (Opt["USAGE"]) {
		if (Opt["VERB"] == "prune")
			usage_prune()
		else
			usage()
	}
	if (!Source["DS"] && !Target["DS"]) { usage("no datasets defined") }

	if (Opt["MATCH_PIPE"]) {
		Target["match_bookmarks"]  = 1
		Target["ds_length"]        = length(Target["DS"]) + 1
		Source["ds_length"]        = length(Source["DS"]) + 1
		Source["list_time"]        = 0
		Target["list_time"]        = 0
		load_exclude_patterns()
		run_zfs_list_target()
		# Continues to process the incoming pipes 'pipe_zfs_list_source()'
	}
	else {
		pipe_zfs_list_source()
		exit
	}
}

# Process inbound pipe from pipe_zfs_list_source()
# The first piped 'zfs list' row could lock execution of the above, so ignore it.
NR > 1 {
	load_zfs_list_row(Source)
}

END {
	if (Opt["MATCH_PIPE"]) {
		process_datasets()

		if (Opt["VERB"] == "prune") {
			analyze_prune_candidates()
			output_prune()
		} else {
			get_info()
			summary()
		}
		stop(0)
	}
}
