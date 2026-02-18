#!/usr/bin/awk -f
#
# zelta-backup.awk, zelta (replicate|backup|sync|rotate|clone) - replicates remote or local trees
#   of zfs datasets
#
# After using "zelta match" to identify out-of-date snapshots on the target, this script creates
# replication streams to synchronize the snapshots of a dataset and its children. This script is
# useful for backup, migration, and failover scenarios. It intentionally does not have a rollback
# feature, and instead uses a "rotate" feature to rename and clone the diverged replica.
#
# CONCEPTS
# endpoint or ep: "SRC" or "TGT"
# dataset or ds: A specific dataset
# tree: A dataset and its children
# ds_snap: A specific snapshot, such as a replication source
# ds_suffix: The dataset suffix/child element of the dataset tree (formerly 'rel_name')
#
# GLOBALS
# Opt: User settings (see the 'zelta' sh script and zelta-opts.tsv)
# NumDS: Number of datasets in the tree
# DSList: List of "ds_suffix" elements in replication order
# FailedProps: List of "permission denied" messages received during sync
# Dataset: Properties of each dataset, indexed by: ("ENDPOINT", ds_suffix, element)
# 	[zfsprops]:  ZFS properties from the property-source 'local' or 'none'
# 	exists
# 	earliest_snapshot
# 	latest_snapshot
#
# DSPair: Derived properties comparing a dataset and its replica: (ds_suffix, element)
# 	match:           the common snapshot or bookmark between a pair
# 	source_start:    the incremental or intermediate source snapshot/bookmark
# 	source_end:      the source snapshot intended to be synced
#
# DSTree: Properties about the global state or overrides
# 	ep,"count"       number of datasets for each endpoint
# Summary: Totals and other summary information
#
# FOR RELEASE NOTES:
# Dropped: summary message "replicationErrorCode"
# Changed "replicate" terminology to "sync" to avoid confusion with 'zfs send --replicate'


## Usage
########

# We follow zfs's standard of only showing short options when available
function usage(message,		_ep_spec, _verb, _clone, _revert) {
	_src_spec = "[[user@]host:]pool[/dataset[@snapshot]]"
	_ep_spec  = "[[user@]host:]pool/dataset[@snapshot]"
	_verb     = Opt["VERB"]
	_revert   = (_verb == "revert")
	_clone    = (_verb == "clone")
	if (message) print message                                         > STDERR
	printf "usage: " _verb " [OPTIONS] "                               > STDERR
	print _revert ? "ENDPOINT" : "SOURCE TARGET"                       > STDERR
	print "\nRequired Arguments:"                                      > STDERR
	if (_revert)
		print "  ENDPOINT  " _ep_spec                                  > STDERR
	else {
		print "  SOURCE    " _src_spec                                 > STDERR
		# print "  TARGET    " _ep_spec                                  > STDERR
	}
	if (_clone) {
		printf "Clone endpoints require the same 'user', "             > STDERR
		print "'host', and 'pool'."                                    > STDERR
	}
	print "\nCommon Options:"                                          > STDERR
        print "  -v, -vv                    Verbose/debug output"      > STDERR
        print "  -q, -qq                    Suppress warnings/errors"  > STDERR
        print "  -j, --json                 JSON output"               > STDERR
	if (!_revert) {
		print "  --snapshot                 Always create snapshot"    > STDERR
		print "  --snap-name NAME           Set snapshot name"         > STDERR
	}
	if (!_clone) {
		print "\nAdvanced Options:"                                    > STDERR
		if (_verb == "backup")
			print "  -i, --incremental          Incremental sync"      > STDERR
		print "  -d, --depth NUM            Set max dataset depth"     > STDERR
	}

	print "\nFor complete documentation:  zelta help " _verb           > STDERR
	print "                             https://zelta.space"           > STDERR

	exit 1
}

## Loading and setting properties
#################################

# After a sync or snapshot, update the latest snapshot for further action or validation
function update_latest_snapshot(endpoint, ds_suffix, snap_name,		_idx, _src_latest) {
	_idx = endpoint SUBSEP ds_suffix
	_src_latest = Dataset["SRC", ds_suffix, "latest_snapshot"]
	# TO-DO: This should be its own function
	# The source is updated via a snapshot
	if (endpoint == "SRC") {
		# If this is the first snapshot for the source, update the snap counter
		DSTree["source_snap_num"]++
		if (!_src_latest)
			Dataset[_idx, "earliest_snapshot"] = snap_name
			Dataset[_idx, "next_snapshot"] = snap_name
		# If we previously had a match
		if (DSPair[ds_suffix, "match"] == _src_latest) {
			Dataset[_idx, "next_snapshot"] = snap_name
		}
		Dataset[_idx, "latest_snapshot"]  = snap_name
	}
	# The target is updated via a sync and becomes our new match
	else if (endpoint == "TGT") {
		DSPair[ds_suffix, "match"] = snap_name
		Dataset[_idx, "latest_snapshot"] = snap_name
		if (snap_name == _src_latest) {
			Dataset["SRC", ds_suffix, "next_snapshot"] = ""
			DSTree["syncable"]--
			Action[ds_suffix, "block_reason"] = "up-to-date"
			Action[ds_suffix, "can_sync"] = 0
		}
		# If the snapshot transferred isn't the latest, this is a 2-pass intermediate sync
		else
			compute_send_range(ds_suffix)
	}
}

# Evaluate properties needed for snapshot decision and sync options
function check_snapshot_needed(endpoint, ds_suffix, prop_key, prop_val) {
	if (endpoint == "SRC") {
		if (Dataset[endpoint, ds_suffix, "written"]) {
			Summary["sourceWritten"] += prop_val
			DSPair[ds_suffix, "source_is_written"] += prop_val
			DSTree["snapshot_needed"] = SNAP_WRITTEN
		}
	}
}


# Load zfs properties for an endpoint
function load_properties(ep,		_ds, _cmd_arr, _cmd, _cmd_id, _ds_suffix, _idx, _seen, _log_level) {
	_cmd_id               = "zfs get"
	_ds                   = Opt[ep "_DS"]
	_cmd_arr["endpoint"]  = ep
	_cmd_arr["ds"]        = rq(Opt[ep"_REMOTE"],_ds)
	_cmd_arr["props"]     = "all"
	if (Opt["DEPTH"]) _cmd_arr["flags"] = "-d" (Opt["DEPTH"]-1)
	_cmd = build_command("PROPS", _cmd_arr)
	report(LOG_INFO, "checking properties for " Opt[ep"_ID"])
	report(LOG_DEBUG, "`"_cmd"`")
	FS = "\t"
	_cmd = _cmd CAPTURE_OUTPUT
	while (_cmd | getline) {
		if (NF == 3 && match($1, "^" _ds)) {
			_ds_suffix = substr($1, length(_ds) + 1)
			_idx = ep SUBSEP _ds_suffix
			_prop_key = $2
			_prop_val = ($3 == "off") ? "0" : $3
			Dataset[_idx, _prop_key] = _prop_val

			check_snapshot_needed(ep, _ds_suffix, _prop_key, _prop_val)
			if (!_seen[_idx]++) {
				Dataset[_idx, "exists"]++
				DSTree[ep, "count"]++
			}
		}
		else if (/dataset does not exist/) {
			close(_cmd)
			return 0
		}
		else {
			_log_level = log_common_command_feedback()
			if (_log_level == LOG_ERROR) stop(1)
		}
	}
	close(_cmd)
	return Dataset[ep, "", "exists"]
}


## Load snapshot information from `zelta match`
###############################################

# Imports a 'zelta match' row into DSPair and Dataset
function parse_zelta_match_row(		_ds_suffix, _src_idx, _tgt_idx) {
	if (NF == 5) {
		# Indexes
		_ds_suffix				= $1
		_src_idx				= "SRC" SUBSEP _ds_suffix
		_tgt_idx				= "TGT" SUBSEP _ds_suffix

		# 'zelta match' columns
		DSList[++NumDS]				= _ds_suffix
		DSPair[_ds_suffix, "match"]		= $2
		Dataset[_src_idx, "next_snapshot"]	= $3
		Dataset[_src_idx, "latest_snapshot"]	= $4
		Dataset[_tgt_idx, "latest_snapshot"]	= $5
	}
	else {
		if ($1 == "SOURCE_LIST_TIME:")		Summary["sourceListTime"] += $2
		else if ($1 == "TARGET_LIST_TIME:")	Summary["targetListTime"] += $2
		else report(LOG_WARNING, "unexpected `zelta match` output: "$0)
		return
	}
}

# Run 'zfs match' and pass to parser
function load_snapshot_deltas(_cmd_arr, _cmd) {
	FS = "\t"
	if (!DSTree["target_exists"])
		_cmd_arr["command_prefix"]	= "ZELTA_TGT_ID=''"
	if (Opt["DRYRUN"])
		_cmd_arr["command_prefix"]	= str_add(_cmd_arr["command_prefix"], "ZELTA_DRYRUN=''")
	# Depth is already in the environment
	if (Opt["DEPTH"])
		_cmd_arr["flags"]		= "-d" Opt["DEPTH"]
	_cmd					= build_command("MATCH", _cmd_arr)
	report(LOG_INFO, "checking replica deltas")
	report(LOG_DEBUG, "`"_cmd"`")
	_cmd 					= _cmd CAPTURE_OUTPUT
	while (_cmd | getline) parse_zelta_match_row()
	close(_cmd)
}

## Compute derived data from properties and snapshots
#####################################################

# Computes 'zfs send' snapshot parameters
function compute_send_range(ds_suffix,		_ds_suffix, _src_idx, _final_ds_snap) {
	_ds_suffix	= ds_suffix
	_src_idx	= "SRC" SUBSEP _ds_suffix
	_final_ds_snap	= ""

	# Calculate the single or "incremental/intermediate target" 'zfs send' snapshot

	# Use the overriden value from a snapshot or parameter
	if (DSTree["final_snapshot"])
		_final_ds_snap	= DSTree["final_snapshot"]
	# In intermediate '-I' mode, use the earliest snapshot for a full pass
	else if (Opt["SEND_INTR"] && !DSPair[_ds_suffix, "match"])
		_final_ds_snap	= Dataset[_src_idx, "next_snapshot"]
	# 'zelta rotate' wants the next-available snapshot
	else if (Opt["VERB"] == "rotate")
		_final_ds_snap	= Dataset[_src_idx, "next_snapshot"]
	# For second pass or -i incremental mode, get up-to-date
	else
		_final_ds_snap	= Dataset[_src_idx, "latest_snapshot"]

	# 'zfs send' arguments: [-[Ii] source_start] source_end
	DSPair[_ds_suffix, "source_start"]      = DSPair[_ds_suffix, "match"]
	DSPair[_ds_suffix, "source_end"]	= _final_ds_snap
}

# Report block reasons for datasets that couldn't be synced
function explain_sync_status(ds_suffix,		_tgt_ds) {
	_tgt_ds = Opt["TGT_DS"] ds_suffix
	# Only report if there's a block reason to explain
	if (Action[ds_suffix, "block_reason"])
		report(LOG_NOTICE, Action[ds_suffix, "block_reason"]": " _tgt_ds)
}

# Ensure source snapshots are available and load snapshot relationship data
function validate_snapshots(		_i, _ds_suffix, _src_idx, _match, _src_exists, _src_latest) {
	create_source_snapshot()
	load_snapshot_deltas()
	for (_i in DSList) {
		_ds_suffix	= DSList[_i]
		_src_idx	= "SRC" SUBSEP _ds_suffix
		_src_exists	= Dataset[_src_idx, "exists"]
		_src_latest	= Dataset[_src_idx, "latest_snapshot"]
		_match		= DSPair[_ds_suffix, "match"]
		if (_src_exists && !_src_latest)
			DSTree["snapshot_needed"] = SNAP_MISSING
		else if (Opt["VERB"] == "rotate") {
			if (_match && (_match == _src_latest))
				DSTree["snapshot_needed"] = SNAP_LATEST
		}
	}
	create_source_snapshot()
	for (_i in DSList) {
		_src_idx = "SRC" SUBSEP DSList[_i]
		if (Dataset[_src_idx, "latest_snapshot"])
			DSTree["source_snap_num"]++
	}
	if (!DSTree["source_snap_num"])
		stop(1, "source '" Opt["SRC_ID"]"' has no snapshots")
}

function compute_eligibility(           _i, _ds_suffix, _src_idx, _tgt_idx,
                                        _has_next, _has_match, _tgt_exists,
                                        _match, _src_latest, _tgt_latest) {
	# Reset counters
	DSTree["syncable"]         = 0
	DSTree["needs_snapshot"]   = 0
	DSTree["up_to_date"]	   = 0
	delete Action

		for (_i = 1; _i <= NumDS; _i++) {
			_ds_suffix       = DSList[_i]
		_src_idx        = "SRC" SUBSEP _ds_suffix
		_tgt_idx        = "TGT" SUBSEP _ds_suffix

		# Gather all the state we need in one place
		_has_next       = !!Dataset[_src_idx, "next_snapshot"]
		_has_match      = !!DSPair[_ds_suffix, "match"]
		_src_exists     = !!Dataset[_src_idx, "exists"]
		_tgt_exists     = !!Dataset[_tgt_idx, "exists"]
		_match          = DSPair[_ds_suffix, "match"]
		_src_latest     = Dataset[_src_idx, "latest_snapshot"]
		_tgt_latest     = Dataset[_tgt_idx, "latest_snapshot"]

		# Get source_start and source_end
		compute_send_range(_ds_suffix)

		# CASE 1: Datasets are missing

		# No source
		if (!_src_exists) {
			Action[_ds_suffix, "block_reason"] = "no source"
			DSTree["no_source_count"]++
			continue
		}


		# No source snapshot
		if (!_src_latest) {
			Action[_ds_suffix, "block_reason"] = "no source snapshot"
			DSTree["needs_snapshot"]++
			continue
		}

		# No target means we have a full backup only
		if (!_tgt_exists) {
			Action[_ds_suffix, "can_sync"] = 1
			DSTree["syncable"]++
			continue
		}

		# CASE 2: Target exists - check for incremental sync

		# Resume token overrides all other checks
		if (Dataset[_tgt_idx, "receive_resume_token"]) {
			Action[_ds_suffix, "can_sync"] = 1
			Action[_ds_suffix, "resumable"] = 1
			DSTree["syncable"]++
			DSTree["resumable"]++
			continue
		}

		if (!_tgt_latest) {
			Action[_ds_suffix, "block_reason"] = "no snapshot; target diverged"
			continue
		}

		if (!_has_match) {
			Action[_ds_suffix, "block_reason"] = "no common snapshot (diverged)"
			if (Dataset[_src_idx, "origin"]) {
				Action[_ds_suffix, "check_source_origin"] = 1
				DSTree["snapshots_diverged"]++
			}
			continue
		}

		# Case 3: We have matches

		# Match is latest on source - nothing to sync
		if (_match == _src_latest) {
			# Target has local changes
			if (Dataset[_tgt_idx, "written"]) {
				Action[_ds_suffix, "block_reason"] = "target has local writes"
				continue
			}
			# TO-DO: Improve verbose output
			#Action[_ds_suffix, "block_reason"] = "up-to-date"
			DSTree["up_to_date"]++
			continue
		}

		# Target is ahead or has diverged otherwise
		if (_match != _tgt_latest) {
			Action[_ds_suffix, "block_reason"] = "target has diverged"
			Action[_ds_suffix, "can_rotate"] = 1
			DSTree["rotatable"]++
			continue
		}
		# If we got here, we can sync
		Action[_ds_suffix, "can_sync"] = 1
		Action[_ds_suffix, "can_rotate"] = 1
		DSTree["syncable"]++
		DSTree["rotatable"]++
	}
}

## Create snapshot with `zfs snapshot`
######################################

# Decide whether or not to take a snapshot; if so, returns a reason
function should_snapshot(		_snapshotting) {
	# Only attempt a snapshot once
        if (DSTree["snapshot_attempted"]) return
	if (Opt["DRYRUN"])
		_snapshotting = "would snapshot: "
	else
		_snapshotting = "snapshotting: "

	# Snapshot mode is "ALWAYS" or provide a reason
	if (Opt["SNAP_MODE"] == "ALWAYS")
		return _snapshotting
	else if (Opt["SNAP_MODE"] != "IF_NEEDED")
		return 0
	else if (DSTree["snapshot_needed"] == SNAP_WRITTEN)
		return "source is written; " _snapshotting
	else if (DSTree["snapshot_needed"] == SNAP_MISSING)
		return "missing source snapshot; " _snapshotting
	else if (DSTree["snapshot_needed"] == SNAP_LATEST)
		return "action requires a snapshot delta; " _snapshotting
	else return 0
}

# This function replaces the original 'zelta snapshot' command
function create_source_snapshot(force_snap,	_snap_name, _ds_snap, _cmd_arr, _cmd, _snap_failed, _should_snap, _i) {
	_should_snap = force_snap ? force_snap : should_snapshot()
	if (_should_snap) {
		_snap_name = get_snap_name()
		report(LOG_NOTICE, _should_snap _snap_name)
	} else return

        DSTree["snapshot_needed"] = 0
        DSTree["snapshot_attempted"] = 1

	_ds_snap = Opt["SRC_DS"] _snap_name
	_cmd_arr["endpoint"] = "SRC"
	_cmd_arr["ds_snap"] = _ds_snap
	_cmd = build_command("SNAP", _cmd_arr)

	if (Opt["DRYRUN"]) {
		report(LOG_NOTICE, "+ "_cmd)
		return 1
	}


	_cmd = _cmd CAPTURE_OUTPUT
	report(LOG_DEBUG, "`"_cmd"`")
	while (_cmd | getline) {
		if (/permission denied/) {
			_snap_failed++
			report(LOG_WARNING, "permission denied snapshotting: " Opt["SRC_DS"])
			break
		} else {
			_snap_failed++
			report(LOG_WARNING, "unexpected `zfs snapshot` output: "$0)
		}
	}
	close(_cmd)
	# If there's unexpected output, rely on `zelta match` to compute a final snapshot
	if (!_snap_failed) {
		# We only want to attempt to take a snapshot at most once
		DSTree["snapshot_attempted"]++
		for (_i = 1; _i <= NumDS; _i++) {
			update_latest_snapshot("SRC", DSList[_i], _snap_name)
		}
		return 1
	}
	else return 0
}

## Dataset and properties validation
####################################


function dataset_exists(ep, ds,		_cmd_arr, _cmd, _ds_exists, _remote) {
	_remote = Opt[ep"_REMOTE"]
	_cmd_arr["endpoint"]	= ep
	_cmd_arr["ds"]		= rq(_remote, ds)
	_cmd = build_command("CHECK", _cmd_arr)
	report(LOG_INFO, "checking for existence of "ep" dataset: "ds)
	report(LOG_DEBUG, "`"_cmd"`")
	_cmd = _cmd CAPTURE_OUTPUT
	while (_cmd | getline) {
		if ($0 == ds) _ds_exists++
		else return
	}
	close(_cmd)
	return _ds_exists
}

# We can't perform a sync without a parent dataset, and 'zfs create' produces no output when it succeeds,
# so this is a perfect test and it's a requirement if the CHECK_PARENT option is given. Unfortunately,
# a nasty ZFS bug means that 'zfs create' won't work with readonly datasets, or datasets the user doesn't
# have access to. Thus, we cannot avoid the following gnarly logic.
function validate_target_parent_dataset(		_parent, _cmd, _cmd_arr, _depth, _hit_readonly,
												_attempts, _ds_exists, _retry, _success, _null_arr) {
	if (DSTree["target_exists"]) return 1
	_parent = Opt["TGT_DS"]
	sub(/\/[^\/]*$/, "", _parent) # Strip last child element
	if (!Opt["CREATE_PARENT"]) {
		_parent = Opt["TGT_DS"]
		sub(/\/[^\/]*$/, "", _parent) # Strip last child element
		_ds_exists = dataset_exists("TGT", _parent)
		if (!_ds_exists)
			stop(1, "target has no parent dataset: '"_parent"'")
		else
			return 1
	}

	# Split into function
	_cmd_arr["endpoint"] = "TGT"
	_cmd_arr["ds"]       = Opt["TGT_REMOTE"] ? qq(_parent) : q(_parent)
	_cmd = build_command("CREATE", _cmd_arr)
	_cmd = _cmd CAPTURE_OUTPUT

	report(LOG_INFO, "validating target parent dataset: '"_parent"'")
	report(LOG_DEBUG, "`" _cmd "`")

	# # ZFS bug: 'create -up' on read-only hierarchies fails one level at a time
	_depth = split(_parent, _null_arr, "/")
	_attempts = _depth - 1
	while (_attempts-- > 0) {
		_hit_readonly = 0
		_success = 0
		while (_cmd | getline) {
			if (/Read-only file system/) {
				# THE BUG: This error clears one level of directory creation.
				report(LOG_DEBUG, "`zfs create` read-only bug detected")
				_hit_readonly = 1
			}
			else if (/create ancestors/)
				report(LOG_DEBUG, "attempted to create ancestor(s)")
			else if ($0 ~ "^create "_parent) {
				# This reports even if it fails
				#report(LOG_INFO, "successfully created target parent '"_parent"'")
				#_success = 1
			}
			else if (/^[[:space:]].*=/)
				report(LOG_DEBUG, "property info: '"$0"'")
			else if (/permission denied/)
				stop(1, "permission denied creating target '"_parent"'")
			else if (/no such pool/)
				stop(1, "no such pool in target path: '"_parent"'")
			else
				report(LOG_WARNING, "unexpected `zfs create` output: '"$0"'")
		}
		close(_cmd)
		if (_success || !_hit_readonly) break
		report(LOG_INFO, "incomplete `zfs create`; retrying")
	}
}

# Validate the source dataset
function validate_source_dataset() {
	if (!Source["ID"]) {
		report(LOG_ERROR, "missing endpoint argument")
		usage()
	}
	# Clones must occcur on the same pool
	if ((Opt["VERB"] == "clone") && Opt["TGT_ID"])
		if (DSTree["source_pool"] != DSTree["target_pool"])
			stop(1, "cannot clone: target pool doesn't match source")
	# A valid source is required to continue any subcommand
	if (!load_properties("SRC"))
		stop(1, "source dataset '"Opt["SRC_ID"]"' does not exist")
}

# Ensure the target state is correct
function validate_target_dataset() {
	# Load target properties and validate based on verb
	if (load_properties("TGT")) {
		# Target exists
		DSTree["target_exists"] = 1
		if (Opt["VERB"] == "clone")
			stop(1, "cannot clone: target dataset '"Opt["TGT_ID"]"' already exists")
	} else {
		if (Opt["VERB"] == "rotate")
			stop(1, "cannot '" Opt["VERB"] "': target dataset '"Opt["TGT_ID"]"' does not exist")
	}

	# Now validate the parent exists
	validate_target_parent_dataset()
}

function validate_datasets(	_verb, _src_only, _cloners, _pv_fd) {
	_verb			= Opt["VERB"]
	_cloners["rotate"]	= 1
	_cloners["clone"]	= 1
	_src_only["revert"]	= 1
	load_endpoint(Operands[1], Source)
	load_endpoint(Operands[2], Target)

	# Check command line
	if (Opt["USAGE"])
		usage()
	else if (!NumOperands)
		usage("no endpoints given")
	else if (NumOperands > 2)
		usage("too many operands: " Operands[3])
	else if (NumOperands == 1 && !(_verb in _src_only)) {
                if (_verb in _cloners)
                        report(LOG_ERROR, "did you mean 'zelta revert '" Source["ID"] "' ?")
		usage("missing target endpoint argument")
	}
	else if (NumOperands != 1 && (_verb in _src_only)) {
		report(LOG_ERROR, "did you mean 'zelta rotate' " Source["ID"] " " Target["ID"]" ?")
		usage("'" _verb "' does not take a target argument")
	}

	validate_source_dataset()
	if (!(_verb in _src_only))
		validate_target_dataset()

	# pv for the fans
	# TO-DO: No-op 'zfs send' step to find and insert totals
	if (Opt["RECEIVE_PREFIX"] && !(Source["REMOTE"] && Target["REMOTE"])) {
		ReceivePipe  = Opt["RECEIVE_PREFIX"]
		# Clean up legacy format
		sub(/[| ]*$/, "", ReceivePipe)
		_pv_fd       = " 2>>/dev/tty | "
		ReceivePipe  = ReceivePipe _pv_fd
	}
}


## Assemble `zfs send` command
##############################

# Detect and configure send flags
function get_send_command_flags(ds_suffix, idx,		_f, _idx, _flags, _flag_list) {
	if (Dataset["TGT", ds_suffix, "receive_resume_token"]) {
		_flags = "-t " Dataset["TGT", ds_suffix, "receive_resume_token"]
		return _flags
	}
	if (Opt["SEND_OVERRIDE"])
		return Opt["SEND_OVERRIDE"]
	if (Opt["VERB"] == "replicate")
		_flag_list[++_f]	= Opt["SEND_REPLICATE"]
	else if (Dataset[idx,"encryption"])
     	     _flag_list[++_f]		= Opt["SEND_RAW"]
	else _flag_list[++_f]		= Opt["SEND_DEFAULT"]
	_flags = arr_join(_flag_list)
	return _flags
}

# Detect and configure the '-i/-I ds@snap' phrase
function get_send_command_incr_snap(ds_suffix, idx, remote_ep,	 _flag, _ds_snap, _intr_snap) {
	# Add the -I/-i argument if we can perform an incremental/intermediate sync
	if (DSPair[ds_suffix, "source_origin_match"])
		_ds_snap = DSPair[ds_suffix, "source_origin_match"]
	else if (Dataset["TGT", ds_suffix, "receive_resume_token"])
		return
	else if (DSPair[ds_suffix, "match"])
		_ds_snap = Opt["SRC_DS"] ds_suffix DSPair[ds_suffix, "source_start"]
	else
		return
	_flag		= Opt["SEND_INTR"] ? "-I" : "-i"
	_ds_snap	= remote_ep ? qq(_ds_snap) : q(_ds_snap)
	_intr_snap	= str_add(_flag, _ds_snap)
	return _intr_snap
}

function get_send_command_dataset(ds_suffix, remote_ep,		_ds_snap) {
	if (!Dataset["TGT", ds_suffix, "receive_resume_token"]) {
		_ds_snap = Opt["SRC_DS"] ds_suffix DSPair[ds_suffix, "source_end"]
		_ds_snap = remote_ep ? qq(_ds_snap) : q(_ds_snap)
		return _ds_snap
	}
}

# Assemble a 'zfs send' command with the helpers above
function create_send_command(ds_suffix, idx, remote_ep, 		_cmd_arr, _cmd, _ds_snap) {
	if (!Opt[remote_ep "_REMOTE"]) remote_ep = ""
	_cmd_arr["endpoint"]	= remote_ep
	_cmd_arr["flags"]	= get_send_command_flags(ds_suffix, idx)
	_cmd_arr["intr_snap"]	= get_send_command_incr_snap(ds_suffix, idx, remote_ep)
	_cmd_arr["ds_snap"]	= get_send_command_dataset(ds_suffix, remote_ep)
	_cmd			= build_command("SEND", _cmd_arr)
	return _cmd
}


## Assemble `zfs recv` command
##############################

# Detect and configure recv flags
# Note we need the SOURCE index, not the target's to evaluate some options
function get_recv_command_flags(ds_suffix, src_idx, remote_ep,	_flag_arr, _flags, _i, _origin) {
	if (Opt["RECV_OVERRIDE"])
		return Opt["RECV_OVERRIDE"]
	if (Opt["RECV_DEFAULT"])
		_flag_arr[++_i] = Opt["RECV_DEFAULT"]
	# If this is a full backup, rotate, or rebase, set contextual properties
	if (!DSPair[ds_suffix, "match"] || DSPair[ds_suffix, "target_origin"]) {
		if (ds_suffix == "")
			_flag_arr[++_i]	= Opt["RECV_TOP"]
		if (Dataset[src_idx, "type"] == "volume")
			_flag_arr[++_i]	= Opt["RECV_VOL"]
		if (Dataset[src_idx, "type"] == "filesystem")
			_flag_arr[++_i]	= Opt["RECV_FS"]
	}
	if (Opt["RECV_PROPS_ADD"])
			_flag_arr[++_i]	= Opt["RECV_PROPS_ADD"]
	if (Opt["RECV_PROPS_DEL"])
			_flag_arr[++_i]	= Opt["RECV_PROPS_DEL"]
	if (Opt["RESUME"])
		_flag_arr[++_i]	= Opt["RECV_PARTIAL"]
	if (DSPair[ds_suffix, "target_origin"]) {
		_origin		= DSPair[ds_suffix, "target_origin"] ds_suffix DSPair[ds_suffix, "match"]
		_origin		= rq(remote_ep, _origin)
		_flag_arr[++_i]	= "-o origin=" _origin
	}
	_flags = arr_join(_flag_arr)
	return _flags
}

# Assemble a 'zfs recv' command with the help of the flag builder above
function create_recv_command(ds_suffix, src_idx, remote_ep,		 _cmd_arr, _cmd, _tgt_ds) {
	if (!Opt[remote_ep "_REMOTE"]) remote_ep = ""
	_tgt_ds	                = Opt["TGT_DS"] ds_suffix
	_cmd_arr["endpoint"]    = remote_ep
	_cmd_arr["flags"]       = get_recv_command_flags(ds_suffix, src_idx, remote_ep)
	_cmd_arr["ds"]          = remote_ep ? qq(_tgt_ds) : q(_tgt_ds)
	_cmd                    = build_command("RECV", _cmd_arr)
	if (ReceivePipe) {
		_cmd            = ReceivePipe _cmd
	}
	return _cmd
}

#
## Planning and performing sync
###############################

# Runs a sync, collecting "zfs send" output
function run_zfs_sync(ds_suffix,		_cmd, _stream_info, _message, _ds_snap,
		      				_size, _time, _streams, _sync_msg, _error_msg) {
	# TO-DO: Make 'rotate' logic more explicit
	# TO-DO: Dryrun mode probably goes here
	if (Opt["VERB"] == "rotate" && !Action[ds_suffix, "can_rotate"]) return
	if (Opt["VERB"] != "rotate" && !Action[ds_suffix, "can_sync"]) return
	IGNORE_ZFS_SEND_OUTPUT = "^(incremental|full)"
	IGNORE_RESUME_OUTPUT = "^nvlist version|^\t(fromguid|object|offset|bytes|toguid|toname|embedok|compressok)"
	WARN_ZFS_RECV_PROPS = "cannot receive .* property"
	FAIL_ZFS_SEND_RECV_OUTPUT = "^(cannot receive .* stream|cannot send|missing.*argument)"
	_message            = Source["DS"] ds_suffix
	if (DSPair[ds_suffix, "source_start"])
		_message        = _message DSPair[ds_suffix, "source_start"] "%" substr(DSPair[ds_suffix, "source_end"], 2)
	else
		_message        = _message DSPair[ds_suffix, "source_end"]
	#_message            = _message (DSPair[ds_suffix, "source_start"] ? DSPair[ds_suffix, "source_start"]"%" : "")
	#_message            = _message (DSPair[ds_suffix, "source_end"]
	_ds_snap            = Opt["SRC_DS"] ds_suffix DSPair[ds_suffix, "source_end"]
	_sync_msg           = "synced"
	SentStreamsList[++NumStreamsSent] = _message
	Summary["replicationStreamsSent"]++

	_cmd = get_sync_command(ds_suffix)
	if (Opt["DRYRUN"]) {
		report(LOG_NOTICE, "+ "_cmd)
		return 1
	}

	_cmd = _cmd CAPTURE_OUTPUT
	if (ReceivePipe)
		_cmd = _cmd RECV_PIPE_OUT
	FS="[[:space:]]*"
	report(LOG_DEBUG, "`"_cmd"`")
	while (_cmd | getline) {
		if ($1 == "size") {
			_size = $2
			report(LOG_INFO, "syncing: "h_num($2) " for " _ds_snap)
			Summary["replicationSize"] += $2
		}
		else if ($1 == "received") {
			_streams++
			_time += $5
			Summary["replicationTime"] += $5
			Summary["replicationStreamsReceived"]++
		}
		else if ($1 == "receiving")
			_sync_msg = $2" "$3
		else if (/using provided clone origin/)
			Summary["targetsCloned"]++
		else if (/resume token contents/) {
			# Clear the token
			Dataset["TGT", ds_suffix, "receive_resume_token"] = ""
			Summary["targetsResumed"]++
			report(LOG_BASIC, "resuming transfer for: " _ds_snap)
			report(LOG_INFO, "to abort a failed resume, run: 'zfs receive -A " Opt["SRC_DS"] ds_suffix"'")
		}
		else if ($0 ~ FAIL_ZFS_SEND_RECV_OUTPUT) {
			_error_msg = $0 ": " Opt["TGT_DS"] ds_suffix
			report(LOG_ERROR, _error_msg)
			Summary["replicationErrorCode"] = 2
			break
		}
		else if ($0 ~ WARN_ZFS_RECV_PROPS) {
			report(LOG_DEBUG, $0 ": " Opt["TGT_DS"] ds_suffix)
			if (!FailedProps[$3]++)
				Summary["failed_props"] = str_add(Summary["failed_props"], $3, ",")
		}
		# SIGINFO: Is this still working?
		else if ($1 ~ /:/ && $2 ~ /^[0-9]+$/)
			report(LOG_INFO, $0)
		else if ($0 ~ IGNORE_ZFS_SEND_OUTPUT) {}
		else if ($0 ~ IGNORE_RESUME_OUTPUT) {}
		else if (log_common_command_feedback() == LOG_ERROR) {
			_error_msg = $0
			Summary["replicationErrorCode"] = 2
			break
		}
	}
	close(_cmd)
	if (_streams) {
		# At least one stream has been received, but was it fully successful?
		_message = h_num(_size) " " _sync_msg " received in " _time " seconds"
		if (_streams > 1) _message = str_add(_message, "("_streams" streams)")
		report(LOG_INFO, _message)
		update_latest_snapshot("TGT", ds_suffix, DSPair[ds_suffix, "source_end"])
	} else {
		Action[ds_suffix, "blocked_reason"] = "sync attempted with errors"
		DSTree["syncable"]--
		Action[ds_suffix, "can_sync"] = 0
	}
}

## Construct replication commands
function get_sync_command(ds_suffix,		_src_idx, _tgt_idx, _cmd, _zfs_send, _zfs_recv,
			  			_orchestrate, _push, _pull) {
	_src_idx = "SRC" SUBSEP ds_suffix
	_tgt_idx = "TGT" SUBSEP ds_suffix
	_orchestrate = (Opt["SRC_REMOTE"] && Opt["TGT_REMOTE"])
	_pull = _orchestrate && (Opt["SYNC_DIRECTION"] == "PULL")
	_push = _orchestrate && (Opt["SYNC_DIRECTION"] == "PUSH")

	# If both remotes are the same, it's either local or 'hairpin'
	if (Opt["SRC_REMOTE"] == Opt["TGT_REMOTE"]) {
		_zfs_send 		= create_send_command(ds_suffix, _src_idx)
		_zfs_recv		= create_recv_command(ds_suffix, _src_idx)
		_cmd			=  "{ " _zfs_send "|" _zfs_recv " ; }"
		if (Opt["SRC_REMOTE"])	_cmd = str_add(remote_str("SRC"), dq(_cmd))
	} else if (_pull) {
		_zfs_send		= create_send_command(ds_suffix, _src_idx, "SRC")
		_zfs_recv		= create_recv_command(ds_suffix, _src_idx)
		_cmd			= str_add(remote_str("TGT"), dq(_zfs_send " | " _zfs_recv))
	} else if (_push) {
		_zfs_send		= create_send_command(ds_suffix, _src_idx)
		_zfs_recv		= create_recv_command(ds_suffix, _src_idx, "TGT")
		_cmd			= str_add(remote_str("SRC"), dq(_zfs_send " | " _zfs_recv))
	} else {
		if (_orchestrate && !DSTree["warned_about_proxy"]++)
			report(LOG_WARNING, "syncing remote endpoints through localhost; consider --push or --pull")
		_zfs_send 		= create_send_command(ds_suffix, _src_idx, "SRC")
		_zfs_recv		= create_recv_command(ds_suffix, _src_idx, "TGT")
		_cmd			=  "{ " _zfs_send "|" _zfs_recv " ; }"
	}
	return _cmd
}

## Sync planning
################

## 'zelta rotate'
#################

# Rename a dataset based on the latest or given snapshot name
function rename_dataset(endpoint,		_old_ds, _new_ds, _remote, _snap,
						_cmd_arr, _cmd, _latest, _snap_name) {
	_old_ds				= Opt[endpoint"_DS"]
	_remote				= Opt[endpoint"_REMOTE"]
	_snap				= Opt[endpoint"_SNAP"]
	_latest				= Dataset[endpoint,"","latest_snapshot"]
	_unique_name			= _snap ? _snap : _latest
	_unique_name			= _unique_name ? _unique_name : get_snap_name()
	# This really means, "we don't have a name"
	#if (!_unique_name)
	#	stop(1, "insufficient snapshots to continue: " _old_ds)
	gsub(/@/,"_",_unique_name)
	_new_ds				= _old_ds _unique_name
	# Note that this is an origin dataset, not an origin snapshot
	_cmd_arr["endpoint"]		= endpoint
	_cmd_arr["old_ds"]		= rq(_remote, _old_ds)
	_cmd_arr["new_ds"]		= rq(_remote, _new_ds)
	_cmd				= build_command("RENAME", _cmd_arr)
	if (Opt["DRYRUN"]) {
		report(LOG_NOTICE, "would rename '" _old_ds "' to '" _new_ds"'")
		report(LOG_NOTICE, "+ "_cmd)
		return _new_ds
	}
	report(LOG_NOTICE, "renaming '" _old_ds "' to '" _new_ds"'")
	report(LOG_DEBUG, "`"_cmd"`")
	_cmd = _cmd CAPTURE_OUTPUT
	while (_cmd | getline) {
		if (/cannot (rename|unmount)/)
			stop(1, $0)
		report(LOG_ERROR, "unexpected 'zfs rename' output: " $0)
	}
	close(_cmd)
	return _new_ds
}

function check_origin_match(origin_ds,		_i, _c, _ds_suffix, _origin_arr, _origin_ds, _origin_snap,
			   			_ds_snap_list, _check_list, _cmd_arr, _cmd, _ds_snaps) {
	FS = "[[:space:]]"
	for (_i = 1; _i <= NumDS; _i++) {
		_ds_suffix   = DSList[_i]
		_src_origin  = Dataset["SRC",_ds_suffix,"origin"]
		if (!Dataset["TGT", _ds_suffix, "exists"]) continue
		if (DSPair[_ds_suffix, "match"]) continue
		if (split(_src_origin, _origin_arr, "@") != 2)
			continue
		_origin_ds       = _origin_arr[1]
		_origin_snap     = "@" _origin_arr[2]
		_target_ds_snap  = Opt["TGT_DS"] _ds_suffix _origin_snap

		# TO-DO: Check or warn about multiple origins
		#if (origin_ds != _origin_ds)

		DSPair[_ds_suffix, "source_origin_match"]  = _src_origin
		DSPair[_ds_suffix, "match"]                = _origin_snap
		Action[_ds_suffix, "can_rotate"]           = 1
		_ds_snap_list[++_c]                        = rq(Opt["TGT_REMOTE"], _target_ds_snap)
		DSTree["rotatable"]++
	}

	_cmd_arr["endpoint"]  = "TGT"
	_cmd_arr["ds"]        = arr_join(_ds_snap_list)
	_cmd                  = build_command("CHECK", _cmd_arr)
	report(LOG_INFO, "confirming source origin deltas")
	report(LOG_DEBUG, "`"_cmd"`")
	_cmd = _cmd CAPTURE_OUTPUT
	# TO-DO: Confirm the origins we found are on the target
	while (_cmd | getline) {
		if (/dataset does not exist/)
			report(LOG_WARNING, $3 " is missing and will fail to clone")
	}
	close(_cmd)
	#report(LOG_NOTICE, "full backup may be required for: " _check_list[$1])
	#exit
}

# 'zelta rotate' renames a divergent dataset out of the way
function run_rotate(		_src_ds_snap, _up_to_date, _src_origin_ds, _origin_arr, _num_full_backup,
		    		_origin_ds, _origin_snap, _i, _ds_suffix, _tgt_idx, _can_rotate, _target_origin) {
	_src_ds_snap      = Opt["SRC_DS"] DSPair["","match"]
	_can_rotate       = (NumDS == DSTree["rotatable"])
	_up_to_date       = (NumDS == DSTree["up_to_date"])
	_num_full_backup  = NumDS - DSTree["rotatable"] - DSTree["no_source_count"]

	split(Dataset["SRC","","origin"], _origin_arr, "@")
	_src_origin_ds	= _origin_arr[1]
	_origin_ds	= _origin_arr[1]
	_origin_snap	= _origin_arr[1]

	if (_can_rotate) {
		report(LOG_NOTICE, "rotating from source: " _src_ds_snap)
	} else if (DSTree["snapshots_diverged"]) {
		check_origin_match(_origin_snap)
		#DSTree["rotatable"]
		_can_rotate = (NumDS == DSTree["rotatable"])
		if (_can_rotate)
			report(LOG_NOTICE, "rotating from source origin: " _origin_ds)
	} else {
		if (_up_to_date)
			stop(1, "replica is up-to-date; source snapshot required for rotation: " Opt["SRC_DS"])
		else if (DSTree["rotatable"])
			report(LOG_WARNING, "insufficient snapshots; performing full backup for " _num_full_backup " datasets")
		else {
			report(LOG_ERROR, "target is not a replica of the source")
			stop(1, "to perform a full backup, rename the target dataset or sync to an empty target")
		}
	}

	# Rename target dataset
	_target_origin = rename_dataset("TGT")

	# Sync match from source or source origin, or run a full backup
	for (_i = 1; _i <= NumDS; _i++) {
		_ds_suffix = DSList[_i]
		if (DSPair[_ds_suffix, "match"])
			DSPair[_ds_suffix, "target_origin"] = _target_origin
		else if (Dataset["SRC", _ds_suffix, "exists"])
			Action[_ds_suffix, "can_rotate"] = 1
		run_zfs_sync(DSList[_i])
	}


	# Reload snapshots for confirmation
	delete DSPair
	delete DSTree
	validate_target_dataset()
	load_snapshot_deltas()

	if (DSTree["snapshots_diverged"])
		report(LOG_NOTICE, "ensure the preservation of diverged replica with: zelta backup " _src_origin_ds " " DSTree["target_origin"])
	report(LOG_NOTICE, "to ensure target is up-to-date, run: zelta backup " Source["ID"] " " Target["ID"])
}

function create_recursive_clone(endpoint, origin_ds, new_ds,		_remote, _user_snap, _i, _ds_suffix, _cmd_arr,
			       						_cmd, _idx, _last_snap, _snap, _origin_ds_snap,
									_clone_ds, _ds_count) {
	_remote			= Opt[endpoint"_REMOTE"]
	_user_snap		= Opt[endpoint"_SNAP"]
	for (_i = 1; _i <= NumDS; _i++) {
		_ds_suffix		= DSList[_i]
		_idx			= endpoint SUBSEP _ds_suffix
		_last_snap		= Dataset[_idx, "latest_snapshot"]
		_snap			= _user_snap ? _user_snap : _last_snap
		_origin_ds_snap		= origin_ds _ds_suffix _snap
		_clone_ds		= new_ds _ds_suffix

		if (!_last_snap) continue
		_ds_count++
		_cmd_arr["ds_snap"]	= rq(_remote, _origin_ds_snap)
		_cmd_arr["ds"]		= rq(_remote, _clone_ds)
		_cmd_arr["endpoint"]	= endpoint
		_cmd			= build_command("CLONE", _cmd_arr)
		report(LOG_INFO, "cloning: " _cmd_arr["ds_snap"])
		report(LOG_DEBUG, "`"_cmd"`")
		_cmd			= _cmd CAPTURE_OUTPUT
		FS="[[:space:]]"
		while (_cmd | getline) {
			if (/encryption key not loaded/) report(LOG_INFO, "to mount " clone_ds " load encryption key in " $NF)
			else report(LOG_WARNING, "unexpected 'zfs clone' output: " $0)
		}
	}
	if (_ds_count)
		report(LOG_NOTICE, "cloned " _ds_count "/" NumDS " datasets to " new_ds)
	else
		report(LOG_NOTICE, "no source snapshots to clone")
}

function run_revert(		_ds) {
	# Disable snapshot
	# TO-DO: Add a mechanism to revert to the previous (rather than named) snapshot
	_ds = rename_dataset("SRC")
	create_recursive_clone("SRC", _ds, Opt["SRC_DS"])
	create_source_snapshot("snapshotting: ")
	report(LOG_NOTICE, "to retain replica history, run: zelta rotate '"Opt["SRC_DS"]"' 'TARGET'")
}

# 'zelta backup' and 'zelta sync' orchestration
function run_backup(		_i, _ds_suffix, _syncing, _syncable) {
	_syncing = Opt["DRYRUN"] ? "would sync " : "syncing "
	if (DSTree["syncable"])
		report(LOG_NOTICE, _syncing NumDS " datasets")
	for (_i = 1; _i <= NumDS; _i++) {
		_ds_suffix = DSList[_i]
		# Run first pass sync
		if (Action[_ds_suffix, "can_sync"]) run_zfs_sync(_ds_suffix)
		if (Opt["DRYRUN"]) continue
		# Run second pass sync
		if (Action[_ds_suffix, "can_sync"]) run_zfs_sync(_ds_suffix)
	}
}

function print_summary(		_status, _i, _ds_suffix, _num_streams) {
	if(Summary["failed_props"])
		report(LOG_WARNING, "missing `zfs allow` permissions: " Summary["failed_props"])
	if (DSTree["up_to_date"] == NumDS) {
		_status = (NumDS == 1) ? "dataset" : NumDS " datasets"
		report(LOG_NOTICE, _status" up-to-date")
	} else {
		for (_i = 1; _i <= NumDS; _i++) {
			_ds_suffix = DSList[_i]
			explain_sync_status(_ds_suffix)
		}
	}
	_bytes_sent	= h_num(Summary["replicationSize"])
	_num_streams	= Summary["replicationStreamsReceived"]
	_seconds	= Summary["replicationTime"]
	if (_num_streams) report(LOG_NOTICE, _bytes_sent " sent, "_num_streams" streams received in "_seconds" seconds")
}

# Main planning function
BEGIN {
	if (Opt["USAGE"]) usage()

	## Globals and overrides
	########################

	# Snasphot "IF_NEEDED" reason codes
	SNAP_ALWAYS   = 1
	SNAP_WRITTEN  = 2
	SNAP_MISSING  = 3
	SNAP_LATEST   = 4

	# Telemetry
	GlobalState["vers_major"]  = 1
	GlobalState["vers_minor"]  = 1
	Summary["startTime"]  = sys_time()
	Summary["replicationErrorCode"] = 0
	Summary["replicationStreamsReceived"] = 0
	Summary["replicationStreamsSent"] = 0

	# Misc variables
	DSTree["final_snapshot"]  = Opt["SRC_SNAP"]
	DSTree["target_exists"]   = 0
	DSTree["sync_passes"]     = 0
	split(Opt["SRC_DS"], _src_ds_tree, "/")
	split(Opt["TGT_DS"], _tgt_ds_tree, "/")
	DSTree["source_pool"]     = _src_ds_tree[1]
	DSTree["target_pool"]     = _tgt_ds_tree[1]
	if (Opt["SNAP_MODE"] == "ALWAYS")
		DSTree["snapshot_needed"]	= SNAP_ALWAYS
	if (Opt["VERB"] == "replicate")
		Opt["DEPTH"] = 1
	if (Opt["RECV_PROPS_ADD"]) {
		gsub(/,/, " -o ", Opt["RECV_PROPS_ADD"])
		Opt["RECV_PROPS_ADD"] = "-o " Opt["RECV_PROPS_ADD"]
	}
	if (Opt["RECV_PROPS_DEL"]) {
		gsub(/,/, " -x ", Opt["RECV_PROPS_DEL"])
		Opt["RECV_PROPS_DEL"] = "-x " Opt["RECV_PROPS_DEL"]
	}

	validate_datasets()
	validate_snapshots()
	compute_eligibility()

	if (Opt["VERB"] == "clone")		create_recursive_clone("SRC", Opt["SRC_DS"], Opt["TGT_DS"])
	else if (Opt["VERB"] == "revert")	run_revert()
	else if (Opt["VERB"] == "rotate")	run_rotate()
	else					run_backup()

	Summary["endTime"]			= sys_time()
	Summary["runTime"]			= Summary["endTime"] - Summary["startTime"]

	compute_eligibility()
	print_summary()

	stop(Summary["replicationErrorCode"])
}
