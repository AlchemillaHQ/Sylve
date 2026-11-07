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
	print "\tmatch [-Hp] [FILTERS] [-o field[,...]] SOURCE TARGET\n" > STDERR
	print "The following fields are supported:\n"                    > STDERR
	printf usage_table"\n", "PROPERTY", "VALUES"                     > STDERR
	for(_counter in ColInfo) {
		_key = ColList[++_c]
		if (ColWarn[_key])
			continue
		printf usage_table, _key, ColInfo[_key] > STDERR
	}
	print "\nFilter options:"                                                    > STDERR
	print "\t--depth num          Limit to 'num' dataset levels" > STDERR
	print "\t--exclude pattern    Exclude datasets or snapshots matching pattern" > STDERR
	print "\t--include pattern    Include only datasets or snapshots matching pattern" > STDERR
	print "\nSizes are specified in bytes with standard units such as K, M, G, etc.\n"         > STDERR
	print "SOURCE and TARGET endpoints are in the form: [user@host:]pool[/dataset/][@snap]\n"  > STDERR
	print "For complete documentation:  zelta help [<topic>]"                                  > STDERR
	print "                             https://zelta.space"                                   > STDERR
	stop(1)
}

function usage_prune(message) {
	STDERR = "/dev/stderr"
	printf (message ? message "\n" : "") "usage:"                                                 > STDERR
	print "\tprune [OPTIONS] ENDPOINT\n"                                                          > STDERR
	print "Reports snapshot prune candidates on ENDPOINT.\n"                                      > STDERR
	print "Guard Options:"                                                                        > STDERR
	print "  --match-endpoint=GUARD  Compare against GUARD for match protection"                  > STDERR
	print "  --prune-guard=latest    Protect latest match with GUARD"                             > STDERR
	print "  --prune-guard=unsynced  Only consider pruning snapshots replicated with GUARD"       > STDERR
	print "  --no-prune-guard        Disable match-endpoint safety checks"                        > STDERR
	print "Filter Options:"                                                                       > STDERR
	print "  --depth=NUM             Set max dataset depth"                                       > STDERR
	print "  --exclude=pattern       Exclude datasets or snapshots matching pattern"              > STDERR
	print "  --include=pattern       Include only datasets or snapshots matching pattern"         > STDERR
	print "  --prune-name=pattern    Select snapshot names: zfsnap, truenas, sanoid, or glob"       > STDERR
	print "Retention Options:"                                                                    > STDERR
	print "  --prune-num=N           Minimum number of snapshots to keep"                         > STDERR
	print "  --prune-time=T          Keep snapshots newer than duration T (from current time)"    > STDERR
	print "  --prune-size=N          Select oldest eligible snapshots until N bytes are reached"  > STDERR
	print "  --prune-grid=GRID       GFS grid such as '30x1 day, 52x1 week, 1 year', measured"    > STDERR
	print "                          from  the latest unfiltered snapshot"
	print "Default: '--prune-num=30 --prune-time=1month'\n"                                       > STDERR
	print "To review and destroy snapshots, use 'zprune'.\n"                                      > STDERR
	print "For complete documentation:  zelta help prune"                                         > STDERR
	print "                             https://zelta.space"                                      > STDERR
	stop(1)
}


## Command Building
###################

# Default to 'zfs list ... -o written', but implicitly avoid since it's slow
function add_written(endpoint) {
	if (Opt["VERB"] == "prune") {
		if (endpoint["ID"] == Source["ID"])
			return ",written,creation,used,referenced,clones"
		return ",written,creation,used,referenced"
	}
	if (Opt["LIST_WRITTEN"] && Opt["PROPLIST"]) {
		if (Opt["PARSABLE"] && (Opt["PROPLIST"] !~ /(all|written|size)/))
			return ""
	}
	return Opt["LIST_WRITTEN"] ? ",written,creation,used" : ""
}

function add_ivsetguid() {
	return NeedMatchIVSet ? ",ivsetguid" : ""
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
	_cmd_arr["props"]	= "name,guid" add_ivsetguid() add_written(endpoint)
	_cmd_arr["remote"]	= get_remote_cmd(endpoint)
	_cmd_arr["ds"]		= rq(_remote, _ds)
	_cmd_arr["flags"]	= "-t all -Screatetxg"
	if (Opt["DEPTH"])
		_cmd_arr["flags"] = str_add(_cmd_arr["flags"], "-d" Opt["DEPTH"])
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
function process_row(ep,		_name, _guid, _ivsetguid, _written, _referenced, _clones, _name_suffix, _ds_suffix, _savepoint,
					_type, _ep_id, _ds_id, _ds_snap, _row_id, _tmp_arr, _num_snaps,
					_all_snap_idx, _field, _prune_filtered) {
	# Read the row data
	_name      = $1
	_guid      = $2
	_field     = 2
	if (NeedMatchIVSet)
		_ivsetguid = $++_field
	if (Opt["LIST_WRITTEN"] || (Opt["VERB"] == "prune")) {
		_written   = $++_field
		_creation  = $++_field
		_used      = $++_field
	}
	if (Opt["VERB"] == "prune") {
		_referenced = $++_field
		if (ep["ID"] == Source["ID"])
			_clones = $++_field
	}

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
	if ((_type == IS_SNAPSHOT) && (_ep_id == Source["ID"]))
		_all_snap_idx = ++AllSnapIdx[_ds_id]

	# Check for exclusion
	if (_type == IS_DATASET) {
		if (is_ds_excluded(_name))
			return
		if (regex_loop(_ds_suffix, ExcludeDSPattern, ExcludeDSPattern["count"]))
			return
		if (!is_ds_included(_name) && !is_ds_included(_ds_suffix))
			return
	}
	if ((_type == IS_SNAPSHOT) && (_ep_id == Source["ID"])) {
		if (regex_loop(_savepoint, ExcludeSnapPattern, ExcludeSnapPattern["count"]) ||
		    !is_snap_or_ds_included(_savepoint, _name, _ds_suffix)) {
			if (Opt["VERB"] != "prune")
				return
			_prune_filtered = 1
		}
	}

	Row[_row_id, "exists"]     = 1
	Row[_row_id, "guid"]       = _guid
	Row[_row_id, "ivsetguid"]  = _ivsetguid
	Row[_row_id, "written"]    = _written
	Row[_row_id, "creation"]   = _creation
	Row[_row_id, "used"]       = _used
	Row[_row_id, "referenced"] = _referenced
	Row[_row_id, "clones"]     = _clones
	Row[_row_id, "snap_idx"]   = _all_snap_idx
	Row[_row_id, "name"]       = _name
	Row[_row_id, "type"]       = _type
	Row[_row_id, "ds_suffix"]  = _ds_suffix
	Row[_row_id, "prune_filtered"] = _prune_filtered

	# Snapshots will be used for match GUID over bookmarks
	if (!_prune_filtered && (!Guid[_ds_id, _guid] || (_type == IS_SNAPSHOT)))
		Guid[_ds_id, _guid] = _row_id

	# Dataset
	if (_type == IS_DATASET) {
		# Note: 'zfs list -S createtxg' gives us a reverse view of datasets
		_num_ds				= ++ep["num_ds"]
		Dataset[_ep_id, _num_ds]	= _row_id
		Global["written"]		+= _written
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
function load_filter_patterns(opt, ds_assoc, ds_pat_arr, snap_pat_arr,    _i, _n, _pat_arr, _pat) {
	if (!opt) return 0

	_n = split(opt, _pat_arr, ",")
	for (_i = 1; _i <= _n; _i++) {
		_pat = _pat_arr[_i]

		if (_pat ~ /^\/|^\*.*\//)
			ds_pat_arr[++ds_pat_arr["count"]] = glob_to_regex(_pat, "(/.*)?")
		else if (_pat ~ /^@/)
			snap_pat_arr[++snap_pat_arr["count"]] = glob_to_regex(_pat)
		else if (_pat ~ /[\*\?]/)
			report(LOG_WARNING, "invalid filter pattern '"_pat"' must start with '@' or include '/'")
		else
			ds_assoc[_pat] = 1
	}
	return _n

}

# Exclude and include patterns
function load_exclude_patterns() {
	NumExcludeDS = load_filter_patterns(Opt["EXCLUDE"], ExcludeDS, ExcludeDSPattern, ExcludeSnapPattern)
	NumIncludeDS = load_filter_patterns(Opt["INCLUDE"], IncludeDS, IncludeDSPattern, IncludeSnapPattern)
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

function is_ds_included(string,             _i, _pat) {
	if (!Opt["INCLUDE"])
		return 1
	if (!arr_len(IncludeDS) && !IncludeDSPattern["count"])
		return 1
	if (string in IncludeDS)
		return 1
	for (_i in IncludeDS) {
		if (!_i) continue
		_pat = _i "/"
		if (index(string, _pat) == 1)
			return 1
	}
	return regex_loop(string, IncludeDSPattern, IncludeDSPattern["count"])
}

function is_snap_included(savepoint) {
	if (!Opt["INCLUDE"])
		return 1
	return regex_loop(savepoint, IncludeSnapPattern, IncludeSnapPattern["count"])
}

function is_snap_or_ds_included(savepoint, ds_name, ds_suffix) {
	if (!Opt["INCLUDE"])
		return 1
	if (is_snap_included(savepoint))
		return 1
	if ((arr_len(IncludeDS) || IncludeDSPattern["count"]) &&
	    (is_ds_included(ds_name) || is_ds_included(ds_suffix)))
		return 1
	return 0
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
		if (NeedMatchIVSet && Row[src_row, "ivsetguid"] && (Row[src_row, "ivsetguid"] == Row[tgt_row, "ivsetguid"]))
			DSPair[ds_suffix, "match_ivset"] = Row[src_row, "ivsetguid"]
		# Record match index for pruning
		DSPair[ds_suffix, "match_idx"] = snap_idx
	}
}

# Step through snapshots for counters and to find common snapshots
function compare_snapshots(src_row, idx,	_src_row_arr, _ds_suffix, _savepoint, _src_guid, _tgt_ds_id, _tgt_match) {
	if (Row[src_row, "prune_filtered"])
		return

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

function prune_init(		_prune_size, _prune_name) {
	_prune_name = Opt["PRUNE_NAME"]
	if (_prune_name == "zfsnap") _prune_name = "zfsnap_*"
	else if (_prune_name == "truenas") _prune_name = "auto-*"
	else if (_prune_name == "sanoid") _prune_name = "autosnap_*"
	if (!is_null(_prune_name))
		PruneNamePattern = glob_to_regex("@" _prune_name)

	if ((Opt["PRUNE_NUM"] "")  == "" &&
		(Opt["PRUNE_TIME"] "") == "" &&
		(Opt["PRUNE_GRID"] "") == "" &&
		(Opt["PRUNE_SIZE"] "") == "") {
		Opt["PRUNE_NUM"] = 30
		Opt["PRUNE_TIME"] = "30days"
	}

	if (Opt["PRUNE_SIZE"] != "") {
		_prune_size = parse_size(Opt["PRUNE_SIZE"])
		if (_prune_size == "")
			usage_prune("invalid --prune-size: " Opt["PRUNE_SIZE"])
		Opt["PRUNE_SIZE_BYTES"] = _prune_size
	}

	if (Opt["PRUNE_GRID"])
		parse_prune_grid()
}

function parse_prune_grid(	_grid, _parts, _n, _i, _term, _x, _count, _interval) {
	_grid = Opt["PRUNE_GRID"]
	gsub(/[ 	]*x[ 	]*/, "x", _grid)
	_n = split(_grid, _parts, /[,|]+/)
	for (_i = 1; _i <= _n; _i++) {
		_term = _parts[_i]
		sub(/^[ 	]+/, "", _term)
		sub(/[ 	]+$/, "", _term)
		if (!_term) continue
		_x = index(_term, "x")
		if (_x) {
			_count = substr(_term, 1, _x - 1)
			_interval = parse_duration(substr(_term, _x + 1))
		} else {
			_count = -1
			_interval = parse_duration(_term)
		}
		if (((_count != -1) && (_count !~ /^[0-9]+$/)) || !_interval)
			usage_prune("invalid --prune-grid term: " _term)
		PruneGridCount[++NumPruneGrid] = _count
		PruneGridInterval[NumPruneGrid] = _interval
	}
}

function grid_keeps_snapshot(creation, anchor_creation,	_age, _g, _start, _end, _bucket) {
	if (!NumPruneGrid)
		return 0
	_age = anchor_creation - creation
	if (_age < 0)
		return 0
	_start = 0
	for (_g = 1; _g <= NumPruneGrid; _g++) {
		if (PruneGridCount[_g] == -1) {
			if (_age < _start)
				return 0
			_bucket = _g S int((_age - _start) / PruneGridInterval[_g])
			if (!PruneGridBucket[_bucket]++)
				return 1
			return 0
		}
		_end = _start + (PruneGridCount[_g] * PruneGridInterval[_g])
		if ((_age >= _start) && (_age < _end)) {
			_bucket = _g S int((_age - _start) / PruneGridInterval[_g])
			if (!PruneGridBucket[_bucket]++)
				return 1
			return 0
		}
		_start = _end
	}
	return 0
}

function synced_allows_prune(tgt_ds_id, guid, savepoint) {
	if (Opt["PRUNE_GUARD"] == GUARD_NONE)
		return 1
	if (Opt["PRUNE_GUARD"] == GUARD_UNSYNCED)
		return (Guid[tgt_ds_id, guid] && target_has_snap_name(tgt_ds_id, savepoint))
	return 1
}

# Analyze snapshots for pruning eligibility.
# Target safety is controlled by --prune-guard.
function analyze_prune_candidates(		_d, _ds_suffix, _src_ds_id, _tgt_ds_id, _num_snaps,
						_s, _src_row, _savepoint, _guid, _creation,
						_match_idx, _snap_seconds, _min_age, _keep_after_match,
						_grid_anchor_creation,
						_seen_after_match, _eligible_num, _written_total, _prune_estimate, _p,
						_warned_no_target, _warned_no_match,
						_selected_num, SelectedSnap, SelectedSnapIdx,
						EligibleSnap, EligibleSnapIdx, EligibleSnapWritten, EligibleSnapReferenced) {

	prune_init()
	Global["now"] = sys_time()

	if (Opt["PRUNE_TIME"] != "") {
		_snap_seconds = parse_duration(Opt["PRUNE_TIME"])
		if (_snap_seconds == "")
			stop(1, "invalid --prune-time: " Opt["PRUNE_TIME"])
	}
	_min_age = Global["now"] - _snap_seconds
	_keep_after_match = Opt["PRUNE_NUM"]


	for (_d = 1; _d <= NumDSPair; _d++) {
		_ds_suffix = DSPairList[_d]
		_src_ds_id = Source["ID"] S _ds_suffix S ""
		_tgt_ds_id = Target["ID"] S _ds_suffix S ""
		_num_snaps = NumSnaps[_src_ds_id]
		delete PruneGridBucket
		delete EligibleSnap
		delete EligibleSnapIdx
		delete EligibleSnapWritten
		delete EligibleSnapReferenced
		delete SelectedSnap
		delete SelectedSnapIdx
		_eligible_num = 0
		_selected_num = 0
		_written_total = 0
		_prune_estimate = 0
		_seen_after_match = 0

		_match_idx = DSPair[_ds_suffix, "match_idx"]
		if (!_match_idx)
			_match_idx = 0
		_grid_anchor_creation = _match_idx ? Row[Snap[_src_ds_id, _match_idx], "creation"] : Row[Snap[_src_ds_id, 1], "creation"]

		# Analyze snapshots older than match (higher index = older)
		for (_s = _match_idx + 1; _s <= _num_snaps; _s++) {
			_src_row = Snap[_src_ds_id, _s]
			_savepoint = Row[_src_row, "savepoint"]
			_guid = Row[_src_row, "guid"]
			_creation = Row[_src_row, "creation"]

			# Only consider snapshots (not bookmarks)
			if (Row[_src_row, "type"] != IS_SNAPSHOT) continue
			# Narrow pruning only; keep other names visible to guard matching.
			if (Row[_src_row, "prune_filtered"] ||
			    (PruneNamePattern && (_savepoint !~ PruneNamePattern))) {
				KeptSnap[_src_ds_id, ++NumKeptSnap[_src_ds_id]] = _savepoint
				KeptSnapIdx[_src_ds_id, NumKeptSnap[_src_ds_id]] = Row[_src_row, "snap_idx"]
				continue
			}
			_seen_after_match++
			if ((Row[_src_row, "clones"] != "") && (Row[_src_row, "clones"] != "-")) {
				KeptSnap[_src_ds_id, ++NumKeptSnap[_src_ds_id]] = _savepoint
				KeptSnapIdx[_src_ds_id, NumKeptSnap[_src_ds_id]] = Row[_src_row, "snap_idx"]
				continue
			}

			if (!synced_allows_prune(_tgt_ds_id, _guid, _savepoint)) {
				KeptSnap[_src_ds_id, ++NumKeptSnap[_src_ds_id]] = _savepoint
				KeptSnapIdx[_src_ds_id, NumKeptSnap[_src_ds_id]] = Row[_src_row, "snap_idx"]
				continue
			}

			if ((NumPruneGrid && ((_s == 1) || (_s == _num_snaps) || grid_keeps_snapshot(_creation, _grid_anchor_creation))) ||
			    (_keep_after_match != "" && (_keep_after_match > 0) && (_seen_after_match <= _keep_after_match)) ||
			    (_snap_seconds != "" && (_creation >= _min_age))) {
				KeptSnap[_src_ds_id, ++NumKeptSnap[_src_ds_id]] = _savepoint
				KeptSnapIdx[_src_ds_id, NumKeptSnap[_src_ds_id]] = Row[_src_row, "snap_idx"]
				continue
			}

			if (Opt["PRUNE_SIZE_BYTES"]) {
				EligibleSnap[++_eligible_num] = _savepoint
				EligibleSnapIdx[_eligible_num] = Row[_src_row, "snap_idx"]
				EligibleSnapWritten[_eligible_num] = Row[_src_row, "written"]
				EligibleSnapReferenced[_eligible_num] = Row[_src_row, "referenced"]
			} else {
				PruneSnap[_src_ds_id, ++PruneSnapNum[_src_ds_id]] = _savepoint
				PruneSnapIdx[_src_ds_id, PruneSnapNum[_src_ds_id]] = Row[_src_row, "snap_idx"]
			}
		}

		for (_p = _eligible_num; Opt["PRUNE_SIZE_BYTES"] && (_p >= 1); _p--) {
			SelectedSnap[++_selected_num] = EligibleSnap[_p]
			SelectedSnapIdx[_selected_num] = EligibleSnapIdx[_p]
			_written_total += EligibleSnapWritten[_p]
			_prune_estimate = _written_total - EligibleSnapReferenced[_p]
			if (_prune_estimate >= Opt["PRUNE_SIZE_BYTES"])
				break
		}
		for (_p = _selected_num; _p >= 1; _p--) {
			PruneSnap[_src_ds_id, ++PruneSnapNum[_src_ds_id]] = SelectedSnap[_p]
			PruneSnapIdx[_src_ds_id, PruneSnapNum[_src_ds_id]] = SelectedSnapIdx[_p]
		}
	}
}

# List filtered source snapshots after the requested match. output_prune() reverses
# individual snapshot output, so populate newest first to print oldest first.
function analyze_send_range(		_d, _ds_suffix, _src_ds_id, _num_snaps, _s,
					_match_idx, _src_row, _savepoint) {
	for (_d = 1; _d <= NumDSPair; _d++) {
		_ds_suffix = DSPairList[_d]
		_src_ds_id = Source["ID"] S _ds_suffix S ""
		_num_snaps = NumSnaps[_src_ds_id]
		_match_idx = 0

		for (_s = 1; _s <= _num_snaps; _s++) {
			_src_row = Snap[_src_ds_id, _s]
			if (Row[_src_row, "type"] != IS_SNAPSHOT) continue
			if (Row[_src_row, "savepoint"] == Opt["SEND_RANGE"]) {
				_match_idx = _s
				break
			}
		}

		if (!_match_idx)
			continue

		for (_s = 1; _s < _match_idx; _s++) {
			_src_row = Snap[_src_ds_id, _s]
			if (Row[_src_row, "type"] != IS_SNAPSHOT) continue
			PruneSnap[_src_ds_id, ++PruneSnapNum[_src_ds_id]] = Row[_src_row, "savepoint"]
			PruneSnapIdx[_src_ds_id, PruneSnapNum[_src_ds_id]] = Row[_src_row, "snap_idx"]
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

function output_prune_visual(		_d, _ds_suffix, _src_ds_id, _p, _s, _row) {
	for (_d = 1; _d <= NumDSPair; _d++) {
		_ds_suffix = DSPairList[_d]
		_src_ds_id = Source["ID"] S _ds_suffix S ""
		printf "%s\n", Source["ID"] _ds_suffix
		for (_p = 1; _p <= PruneSnapNum[_src_ds_id]; _p++)
			Kill[_src_ds_id, PruneSnapIdx[_src_ds_id, _p]] = 1
		for (_s = NumSnaps[_src_ds_id]; _s >= 1; _s--) {
			_row = Snap[_src_ds_id, _s]
			if (Row[_row, "type"] == IS_SNAPSHOT)
				printf "%s", (Kill[_src_ds_id, Row[_row, "snap_idx"]] ? "❌" : "🔹")
		}
		printf "\n"
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
				if (_prop_opt_arr[_p] == "match_ivset")
					NeedMatchIVSet = 1
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

	GUARD_NONE     = 0
	GUARD_LATEST   = 1
	GUARD_UNSYNCED = 2

	GUARD[""]         = GUARD_LATEST
	GUARD["none"]     = GUARD_NONE
	GUARD["latest"]   = GUARD_LATEST
	GUARD["unsynced"] = GUARD_UNSYNCED

	Opt["PRUNE_GUARD"] = tolower(Opt["PRUNE_GUARD"])
	if (Opt["PRUNE_GUARD"] in GUARD) {
		Opt["PRUNE_GUARD"] = GUARD[Opt["PRUNE_GUARD"]]
	}

	if (! ((Opt["PRUNE_GUARD"] >= 0) && (Opt["PRUNE_GUARD"] <= 2)))
		stop(1, "invalid prune-guard mode: " Opt["PRUNE_GUARD"])

	if (Opt["VERB"] == "prune" && (Opt["MATCH_ENDPOINT"] != "")) {
		if (Operands[2] != "")
			stop(1, "cannot use --match-endpoint with a second positional operand")
		Operands[2] = Opt["MATCH_ENDPOINT"]
		NumOperands = 2
	}

	if (NumOperands < 2)
		Opt["PRUNE_GUARD"] = GUARD_NONE

	load_endpoint(Operands[1], Source)
	load_endpoint(Operands[2], Target)
	load_columns()
	if (Opt["USAGE"]) {
		if (Opt["VERB"] == "prune")
			usage_prune()
		else
			usage()
	}
	if (!is_null(Opt["DEPTH"]) && (Opt["DEPTH"] < 1))
		stop(1, "depth of '"Opt["DEPTH"]"' invalid; must be positive")
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
			if (Opt["SEND_RANGE"])
				analyze_send_range()
			else
				analyze_prune_candidates()
			if (Opt["PRUNE_VISUAL"])
				output_prune_visual()
			else
				output_prune()
		} else {
			get_info()
			summary()
		}
		stop(0)
	}
}
