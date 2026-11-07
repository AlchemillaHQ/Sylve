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
	if (_clone)
		print "SOURCE TARGET [ORIGIN TARGET_BACKUP]"                  > STDERR
	else
		print _revert ? "ENDPOINT" : "SOURCE TARGET"                 > STDERR
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
		print "Four-endpoint clone also backs up the new clone."       > STDERR
	}
	print "\nCommon Options:"                                          > STDERR
        print "  -v, -vv                    Verbose/debug output"      > STDERR
        print "  -q, -qq                    Suppress warnings/errors"  > STDERR
        print "  -j, --json                 JSON output"               > STDERR
	if (!_revert) {
		print "  --snapshot                 Always create snapshot"    > STDERR
		print "  --snap-name NAME           Set snapshot name"         > STDERR
		print "  --snap-prefix PREFIX       Prefix default snapshot names" > STDERR
	}
	if (!_clone) {
		print "\nAdvanced Options:"                                    > STDERR
		if (_verb == "backup")
			print "  -i, --incremental          Incremental sync"      > STDERR
		print "  -d, --depth NUM            Set max dataset depth"     > STDERR
		if (_revert)
			print "  -f, --force                Best-effort recursive revert" > STDERR
		if (_verb == "backup")
			print "  --target-origin ENDPOINT   Back up an existing clone" > STDERR
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
			Action[ds_suffix, "status_info"] = "up-to-date"
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
		if (prop_key == "written" && prop_val) {
			Summary["sourceWritten"] += prop_val
			DSPair[ds_suffix, "source_is_written"] += prop_val
			DSTree["snapshot_needed"] = SNAP_WRITTEN
		}
		else if (prop_key == "snapshots_changed") {
			if (prop_val == "-" || prop_val == "")
				DSTree["source_snapshots_changed_missing"] = 1
			else if (!DSTree["source_snapshots_changed"] || prop_val < DSTree["source_snapshots_changed"])
				DSTree["source_snapshots_changed"] = prop_val
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
			if ((ep == "SRC") && (_prop_key == "encryption") && (_prop_val != "0"))
				DSTree["source_encrypted"] = 1

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
	if ((NF == 5) || (NF == 6)) {
		# Indexes
		_ds_suffix				= $1
		_src_idx				= "SRC" SUBSEP _ds_suffix
		_tgt_idx				= "TGT" SUBSEP _ds_suffix

		# 'zelta match' columns
		DSList[++NumDS]				= _ds_suffix
		DSPair[_ds_suffix, "match"]		= $2
		if (NF == 6) {
			DSPair[_ds_suffix, "match_ivset"]	= $3
			Dataset[_src_idx, "next_snapshot"]	= $4
			Dataset[_src_idx, "latest_snapshot"]	= $5
			Dataset[_tgt_idx, "latest_snapshot"]	= $6
		} else {
			Dataset[_src_idx, "next_snapshot"]	= $3
			Dataset[_src_idx, "latest_snapshot"]	= $4
			Dataset[_tgt_idx, "latest_snapshot"]	= $5
		}
	}
	else {
		if ($1 == "SOURCE_LIST_TIME:")		Summary["sourceListTime"] += $2
		else if ($1 == "TARGET_LIST_TIME:")	Summary["targetListTime"] += $2
		else report(LOG_WARNING, "unexpected `zelta match` output: "$0)
		return
	}
}

# Run 'zfs match' and pass to parser
function ipc_endpoint_env(	_env) {
	_env = "ZELTA_SRC_ID=" q(Opt["SRC_ID"])
	_env = str_add(_env, "ZELTA_SRC_DS=" q(Opt["SRC_DS"]))
	_env = str_add(_env, "ZELTA_SRC_REMOTE=" q(Opt["SRC_REMOTE"]))
	_env = str_add(_env, "ZELTA_TGT_ID=" q(Opt["TGT_ID"]))
	_env = str_add(_env, "ZELTA_TGT_DS=" q(Opt["TGT_DS"]))
	_env = str_add(_env, "ZELTA_TGT_REMOTE=" q(Opt["TGT_REMOTE"]))
	return _env
}

function load_snapshot_deltas(_cmd_arr, _cmd, _cmd_id) {
	FS = "\t"
	_cmd_id = (DSTree["target_exists"] && DSTree["source_encrypted"]) ? "MATCH_IVSET" : "MATCH"
	_cmd_arr["command_prefix"] = ipc_endpoint_env()
	if (!DSTree["target_exists"])
		_cmd_arr["command_prefix"]	= str_add(_cmd_arr["command_prefix"], "ZELTA_TGT_ID=''")
	if (Opt["DRYRUN"])
		_cmd_arr["command_prefix"]	= str_add(_cmd_arr["command_prefix"], "ZELTA_DRYRUN=''")
	# Depth is already in the environment
	if (Opt["DEPTH"])
		_cmd_arr["flags"]		= "-d" Opt["DEPTH"]
	_cmd					= build_command(_cmd_id, _cmd_arr)
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

# Report status notes for datasets that couldn't be synced
function explain_sync_status(ds_suffix,		_tgt_ds) {
	_tgt_ds = Opt["TGT_DS"] ds_suffix
	if (Action[ds_suffix, "status_notice"])
		report(LOG_NOTICE, Action[ds_suffix, "status_notice"]": " _tgt_ds)
	if (Action[ds_suffix, "status_info"])
		report(LOG_INFO, Action[ds_suffix, "status_info"]": " _tgt_ds)
}

# Ensure source snapshots are available and load snapshot relationship data
function validate_snapshots(		_i, _ds_suffix, _src_idx, _match, _src_exists, _src_latest) {
	create_source_snapshot()
	load_snapshot_deltas()
	apply_predicted_source_snapshot()
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
	apply_predicted_source_snapshot()
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
			Action[_ds_suffix, "status_info"] = "no source"
			DSTree["no_source_count"]++
			continue
		}


		# No source snapshot
		if (!_src_latest) {
			Action[_ds_suffix, "status_notice"] = "no source snapshot"
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
			Action[_ds_suffix, "status_notice"] = "no snapshot; target diverged"
			continue
		}

		if (!_has_match) {
			Action[_ds_suffix, "status_notice"] = "no common snapshot (diverged)"
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
				Action[_ds_suffix, "status_notice"] = "target has local writes"
				continue
			}
			# TO-DO: Improve verbose output
			#Action[_ds_suffix, "status_info"] = "up-to-date"
			DSTree["up_to_date"]++
			continue
		}

		# Target is ahead or has diverged otherwise
		if (_match != _tgt_latest) {
			Action[_ds_suffix, "status_notice"] = "target has diverged"
			Action[_ds_suffix, "can_rotate"] = 1
			DSTree["rotatable"]++
			continue
		}
		# If we got here, we can sync
		Action[_ds_suffix, "can_sync"] = 1
		Action[_ds_suffix, "can_rotate"] = 1
		if ((Opt["VERB"] != "replicate") && Dataset[_src_idx, "encryption"] && !DSPair[_ds_suffix, "match_ivset"])
			Action[_ds_suffix, "send_decrypted"] = 1
		DSTree["syncable"]++
		DSTree["rotatable"]++
	}
}

## Create snapshot with `zfs snapshot`
######################################

# Return true when threshold data says IF_NEEDED can safely skip a snapshot
function snapshot_thresholds_allow_skip(	_time, _size, _cutoff, _time_suffix) {
	if (!Opt["SNAP_TIME"] && !Opt["SNAP_SIZE"])
		return 0

	if (Opt["SNAP_TIME"]) {
		_time = parse_duration(Opt["SNAP_TIME"])
		if (_time == "") return 0
		if (DSTree["source_snapshots_changed_missing"]) return 0
		if (!DSTree["source_snapshots_changed"]) return 0
		_time_suffix = substr(Opt["SNAP_TIME"], length(Opt["SNAP_TIME"]), 1)
		_cutoff = (_time_suffix ~ /^[smhdw]$/) ? sys_time() - _time : _time
		if (DSTree["source_snapshots_changed"] < _cutoff) return 0
	}

	if (Opt["SNAP_SIZE"]) {
		_size = parse_size(Opt["SNAP_SIZE"])
		if (_size == "") return 0
		if (Summary["sourceWritten"] >= _size) return 0
	}

	return 1
}

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
	else if (DSTree["snapshot_needed"] == SNAP_WRITTEN && snapshot_thresholds_allow_skip())
		return 0
	else if (DSTree["snapshot_needed"] == SNAP_WRITTEN)
		return "source is written; " _snapshotting
	else if (DSTree["snapshot_needed"] == SNAP_MISSING)
		return "missing source snapshot; " _snapshotting
	else if (DSTree["snapshot_needed"] == SNAP_LATEST)
		return "action requires a snapshot delta; " _snapshotting
	else return 0
}

# Let dry-run planning predict send/receive work caused by a successful snapshot.
function apply_predicted_source_snapshot(		_i) {
	if (!Opt["DRYRUN"] || !DSTree["predicted_snapshot"] || DSTree["predicted_snapshot_applied"])
		return
	for (_i = 1; _i <= NumDS; _i++)
		update_latest_snapshot("SRC", DSList[_i], DSTree["predicted_snapshot"])
	DSTree["predicted_snapshot_applied"] = 1
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
		DSTree["predicted_snapshot"] = _snap_name
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

function load_opt_endpoint(prefix, ep_arr) {
	Opt[prefix "ID"]		= ep_arr["ID"]
	Opt[prefix "REMOTE"]	= ep_arr["REMOTE"]
	Opt[prefix "USER"]		= ep_arr["USER"]
	Opt[prefix "HOST"]		= ep_arr["HOST"]
	Opt[prefix "DS"]		= ep_arr["DS"]
	Opt[prefix "SNAP"]		= ep_arr["SNAP"]
}

function validate_datasets(	_verb, _src_only, _cloners, _pv_fd) {
	_verb			= Opt["VERB"]
	_cloners["rotate"]	= 1
	_cloners["clone"]	= 1
	_src_only["revert"]	= 1
	load_endpoint(Operands[1], Source)
	load_endpoint(Operands[2], Target)
	if ((_verb == "clone") && (NumOperands == 4)) {
		load_endpoint(Operands[3], CloneOriginTarget)
		load_endpoint(Operands[4], CloneBackupTarget)
	}
	if (Opt["ORIGIN_ID"])
		load_endpoint(Opt["ORIGIN_ID"], Origin)

	# Check command line
	if (Opt["USAGE"])
		usage()
	else if (!NumOperands)
		usage("no endpoints given")
	else if ((NumOperands > 2) && !((_verb == "clone") && (NumOperands == 4)))
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

function reset_runtime_state(	_src_ds_tree, _tgt_ds_tree) {
	delete Dataset
	delete DSPair
	delete DSTree
	delete Action
	delete DSList
	NumDS = 0
	DSTree["final_snapshot"]  = Opt["SRC_SNAP"]
	DSTree["target_exists"]   = 0
	DSTree["sync_passes"]     = 0
	split(Opt["SRC_DS"], _src_ds_tree, "/")
	split(Opt["TGT_DS"], _tgt_ds_tree, "/")
	DSTree["source_pool"]     = _src_ds_tree[1]
	DSTree["target_pool"]     = _tgt_ds_tree[1]
	if (Opt["SNAP_MODE"] == "ALWAYS")
		DSTree["snapshot_needed"] = SNAP_ALWAYS
}


## Assemble `zfs send` command
##############################

function send_check_active() {
	return Opt["SEND_CHECK"] && !Opt["SEND_OVERRIDE"]
}

function send_option_error(line) {
	return line ~ /(invalid|illegal|unknown|unrecognized) option|usage: zfs send/
}

function send_option_error_matches_feature(line, feature,	_opt) {
	_opt = line
	if (_opt ~ /(invalid|illegal|unknown|unrecognized) option '[^']*'/) {
		sub(/^.*(invalid|illegal|unknown|unrecognized) option '/, "", _opt)
		sub(/'.*$/, "", _opt)
		return !_opt || _opt == feature || _opt == substr(feature, 2, 1)
	}
	_opt = line
	if (_opt ~ /(invalid|illegal|unknown|unrecognized) option -- ./) {
		sub(/^.* option -- /, "", _opt)
		_opt = substr(_opt, 1, 1)
		return _opt == substr(feature, 2, 1)
	}
	return 1
}

function send_check_feature_longopt(feature, opt) {
	if (feature == "-e")	return opt == "--embed" || opt == "--embedded-data"
	if (feature == "-c")	return opt == "--compressed"
	if (feature == "-L")	return opt == "--largeblock" || opt == "--large-block"
	return 0
}

function send_flags_have_feature(flags, feature, 	_i, _n, _parts, _token, _short) {
	if (is_null(flags)) return 0
	_short = substr(feature, 2, 1)
	_n = split(flags, _parts, " ")
	for (_i = 1; _i <= _n; _i++) {
		_token = _parts[_i]
		if (_token == feature || send_check_feature_longopt(feature, _token))
			return 1
		if (_token ~ /^-[^-]/ && index(substr(_token, 2), _short))
			return 1
	}
	return 0
}

function remove_send_feature_from_short_token(token, feature, 	_i, _short, _out, _char) {
	if (token !~ /^-[^-]/) return token
	_short = substr(feature, 2, 1)
	_out = "-"
	for (_i = 2; _i <= length(token); _i++) {
		_char = substr(token, _i, 1)
		if (_char != _short)
			_out = _out _char
	}
	return (_out == "-") ? "" : _out
}

function remove_send_feature(flags, feature, 	_i, _n, _parts, _token, _out) {
	if (is_null(flags)) return flags
	_n = split(flags, _parts, " ")
	for (_i = 1; _i <= _n; _i++) {
		_token = _parts[_i]
		if (_token == feature || send_check_feature_longopt(feature, _token))
			continue
		_token = remove_send_feature_from_short_token(_token, feature)
		_out = str_add(_out, _token)
	}
	return _out
}

function apply_send_check_flags(flags) {
	if (!send_check_active()) return flags
	if (SendCheckDisabled["-e"])	flags = remove_send_feature(flags, "-e")
	if (SendCheckDisabled["-c"])	flags = remove_send_feature(flags, "-c")
	if (SendCheckDisabled["-L"])	flags = remove_send_feature(flags, "-L")
	return flags
}

function send_check_normalize_flags(ds_suffix, idx, flags) {
	if (!send_check_active()) return flags
	if (Opt["VERB"] == "replicate") return flags
	if (Action[ds_suffix, "send_decrypted"]) return flags
	if (Dataset[idx, "encryption"]) return flags
	if (flags == "--raw") return "-L -c -e"
	return flags
}

function drop_next_send_feature(ds_suffix, error_msg,	_idx, _flags, _i, _feature, _order) {
	if (!send_check_active()) return 0
	if (!send_option_error(error_msg)) return 0
	_idx = "SRC" SUBSEP ds_suffix
	_flags = get_send_command_flags(ds_suffix, _idx)
	split("-e -c -L", _order, " ")
	for (_i = 1; _i <= 3; _i++) {
		_feature = _order[_i]
		if (SendCheckDisabled[_feature]) continue
		if (!send_option_error_matches_feature(error_msg, _feature)) continue
		if (!send_flags_have_feature(_flags, _feature)) continue
		SendCheckDisabled[_feature] = 1
		report(LOG_WARNING, "send-check dropping unsupported zfs send option: " _feature)
		return 1
	}
	return 0
}

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
	else if (Action[ds_suffix, "send_decrypted"]) {
		if (!Action[ds_suffix, "warned_decrypted"]++)
			report(LOG_WARNING, "raw incremental unavailable at " DSPair[ds_suffix, "match"] "; falling back to decrypted send: " Opt["SRC_DS"] ds_suffix)
		_flag_list[++_f]	= Opt["SEND_DECRYPTED"]
	}
	else if (Dataset[idx,"encryption"])
		_flag_list[++_f]		= Opt["SEND_RAW"]
	else _flag_list[++_f]		= Opt["SEND_DEFAULT"]
	_flags = arr_join(_flag_list)
	_flags = send_check_normalize_flags(ds_suffix, idx, _flags)
	_flags = apply_send_check_flags(_flags)
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
	_flag		= (Opt["SEND_INTR"] && !Action[ds_suffix, "force_incremental"]) ? "-I" : "-i"
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

function create_send_check_command(ds_suffix, idx, remote_ep, 		_cmd_arr, _cmd) {
	if (!Opt[remote_ep "_REMOTE"]) remote_ep = ""
	_cmd_arr["endpoint"]	= remote_ep
	_cmd_arr["flags"]	= get_send_command_flags(ds_suffix, idx)
	_cmd_arr["intr_snap"]	= get_send_command_incr_snap(ds_suffix, idx, remote_ep)
	_cmd_arr["ds_snap"]	= get_send_command_dataset(ds_suffix, remote_ep)
	_cmd			= build_command("SEND_CHECK", _cmd_arr)
	return _cmd
}

function run_send_check(		_ds_suffix, _idx, _remote_ep, _cmd, _line, _error_msg, _retry) {
	if (!send_check_active()) return
	if (Opt["DRYRUN"]) return
	_ds_suffix = ""
	if (!Action[_ds_suffix, "can_sync"]) return
	_idx = "SRC" SUBSEP _ds_suffix
	_remote_ep = Opt["SRC_REMOTE"] ? "SRC" : ""
	do {
		_retry = 0
		_cmd = create_send_check_command(_ds_suffix, _idx, _remote_ep)
		report(LOG_DEBUG, "`"_cmd"`")
		_cmd = _cmd CAPTURE_OUTPUT
		while (_cmd | getline _line) {
			if (send_option_error(_line))
				_error_msg = _line
			else if (_line ~ COMMAND_ERROR)
				report(LOG_WARNING, _line)
		}
		close(_cmd)
		if (_error_msg && drop_next_send_feature(_ds_suffix, _error_msg))
			_retry = 1
		_error_msg = ""
	} while (_retry)
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
function sync_error_message(line, ds_suffix) {
	return line (line ~ /:$/ ? " " : ": ") Opt["TGT_DS"] ds_suffix
}

function run_zfs_sync(ds_suffix,		_cmd, _stream_info, _message, _ds_snap,
						_size, _time, _streams, _sync_msg, _error_msg,
						_cmd_status, _sync_error) {
	# TO-DO: Make 'rotate' logic more explicit
	# TO-DO: Dryrun mode probably goes here
	if (Opt["VERB"] == "rotate" && !Action[ds_suffix, "can_rotate"]) return
	if (Opt["VERB"] != "rotate" && !Action[ds_suffix, "can_sync"]) return
	IGNORE_ZFS_SEND_OUTPUT = "^(incremental|full)"
	IGNORE_RESUME_OUTPUT = "^nvlist version|^\t(fromguid|object|offset|bytes|toguid|toname|embedok|compressok)"
	WARN_ZFS_RECV_PROPS = "cannot receive .* property"
	FAIL_ZFS_SEND_RECV_OUTPUT = "^(cannot receive .* stream|cannot send|missing.*argument)"
	FAIL_ZFS_OPTION_OUTPUT = "^(invalid|illegal|unknown|unrecognized) option|^usage:"
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
		track_bookmark_snapshot(ds_suffix)
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
			_error_msg = sync_error_message($0, ds_suffix)
			report(LOG_ERROR, _error_msg)
			_sync_error = 1
			Summary["replicationErrorCode"] = 2
		}
		else if (send_check_active() && send_option_error($0)) {
			_error_msg = sync_error_message($0, ds_suffix)
			report(LOG_ERROR, _error_msg)
			_sync_error = 1
			Summary["replicationErrorCode"] = 2
		}
		else if ($0 ~ FAIL_ZFS_OPTION_OUTPUT) {
			_error_msg = sync_error_message($0, ds_suffix)
			report(LOG_ERROR, _error_msg)
			_sync_error = 1
			Summary["replicationErrorCode"] = 2
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
			_sync_error = 1
			Summary["replicationErrorCode"] = 2
		}
	}
	_cmd_status = close(_cmd)
	if (!_streams && !_sync_error && _cmd_status) {
		_error_msg = "sync command failed before receiving stream: " _ds_snap " -> " Opt["TGT_DS"] ds_suffix
		report(LOG_ERROR, _error_msg)
		_sync_error = 1
		Summary["replicationErrorCode"] = 2
	}
	if (_streams) {
		# At least one stream has been received, but was it fully successful?
		_message = h_num(_size) " " _sync_msg " received in " _time " seconds"
		if (_streams > 1) _message = str_add(_message, "("_streams" streams)")
		report(LOG_INFO, _message)
		update_latest_snapshot("TGT", ds_suffix, DSPair[ds_suffix, "source_end"])
		track_bookmark_snapshot(ds_suffix)
	} else {
		if (_sync_error && drop_next_send_feature(ds_suffix, _error_msg)) {
			Summary["replicationErrorCode"] = 0
			Summary["replicationStreamsSent"]--
			NumStreamsSent--
			run_zfs_sync(ds_suffix)
			return
		}
		if (!_sync_error) {
			_error_msg = "sync failed before receiving stream: " _ds_snap " -> " Opt["TGT_DS"] ds_suffix
			report(LOG_ERROR, _error_msg)
			Summary["replicationErrorCode"] = 2
		}
		Action[ds_suffix, "status_notice"] = "sync attempted with errors"
		DSTree["syncable"]--
		Action[ds_suffix, "can_sync"] = 0
	}
}

function track_bookmark_snapshot(ds_suffix,		_snap) {
	if (!Opt["BOOKMARK_MODE"]) return
	_snap = DSPair[ds_suffix, "source_end"]
	if (!_snap) return
	if (!Bookmark[ds_suffix, "tracked"]++)
		NumBookmarkDS++
	Bookmark[ds_suffix, "snap"] = _snap
	Bookmark[ds_suffix, "target_snap"] = Opt["TGT_DS"] ds_suffix _snap
	Bookmark[ds_suffix, "source_snap"] = Opt["SRC_DS"] ds_suffix _snap
}

function bookmark_name(ds_suffix,		_snap, _name, _prefix) {
	_snap = Bookmark[ds_suffix, "snap"]
	_name = _snap
	sub(/^[@#]/, "", _name)
	_prefix = Opt["BOOKMARK_PREFIX"] ? Opt["BOOKMARK_PREFIX"] : Target["HOST"] "_"
	return Opt["SRC_DS"] ds_suffix "#" _prefix _name
}

function confirm_bookmark_target(ds_suffix,		_tgt_snap, _cmd_arr, _cmd, _confirmed, _failed) {
	_tgt_snap = Bookmark[ds_suffix, "target_snap"]
	_cmd_arr["endpoint"] = "TGT"
	_cmd_arr["ds"] = rq(Target["REMOTE"], _tgt_snap)
	_cmd = build_command("CHECK", _cmd_arr)
	if (Opt["DRYRUN"]) {
		report(LOG_NOTICE, "+ " _cmd)
		return 1
	}
	report(LOG_DEBUG, "`"_cmd"`")
	_cmd = _cmd CAPTURE_OUTPUT
	while (_cmd | getline) {
		if ($0 == _tgt_snap)
			_confirmed = 1
		else {
			_failed = 1
			report(LOG_ERROR, "cannot confirm replicated snapshot: " _tgt_snap ": " $0)
		}
	}
	close(_cmd)
	if (!_confirmed && !_failed)
		report(LOG_ERROR, "cannot confirm replicated snapshot: " _tgt_snap)
	return _confirmed
}

function create_source_bookmark(ds_suffix,		_cmd_arr, _cmd, _src_snap, _bookmark, _failed) {
	_src_snap = Bookmark[ds_suffix, "source_snap"]
	_bookmark = bookmark_name(ds_suffix)
	_cmd_arr["endpoint"] = "SRC"
	_cmd_arr["source_snap"] = rq(Source["REMOTE"], _src_snap)
	_cmd_arr["bookmark"] = rq(Source["REMOTE"], _bookmark)
	_cmd = build_command("BOOKMARK", _cmd_arr)
	report(LOG_INFO, "bookmarking: " _bookmark)
	if (Opt["DRYRUN"]) {
		report(LOG_NOTICE, "+ " _cmd)
		return 1
	}
	report(LOG_DEBUG, "`"_cmd"`")
	_cmd = _cmd CAPTURE_OUTPUT
	while (_cmd | getline) {
		_failed = 1
		report(LOG_ERROR, "cannot bookmark replicated snapshot: " _src_snap ": " $0)
	}
	close(_cmd)
	return !_failed
}

function bookmark_failed() {
	if (!Summary["replicationErrorCode"])
		Summary["replicationErrorCode"] = 1
}

function run_bookmark(		_i, _ds_suffix, _snapshots, _bookmarked) {
	if (!Opt["BOOKMARK_MODE"]) return
	if (RunVerb == "revert" || RunVerb == "clone") return
	if (Opt["BOOKMARK_MODE"] != 1)
		stop(1, "invalid bookmark mode: " Opt["BOOKMARK_MODE"])
	if (!NumBookmarkDS) return

	for (_i = NumDS; _i >= 1; _i--) {
		_ds_suffix = DSList[_i]
		if (!Bookmark[_ds_suffix, "tracked"]) continue
		if (!confirm_bookmark_target(_ds_suffix)) {
			bookmark_failed()
			continue
		}
		if (create_source_bookmark(_ds_suffix))
			_bookmarked++
		else
			bookmark_failed()
	}
	_snapshots = (_bookmarked == 1) ? "snapshot" : "snapshots"
	if (!Opt["DRYRUN"])
		report(LOG_NOTICE, _bookmarked " " _snapshots " bookmarked")
}

function validate_bookmark_mode() {
	if (!Opt["BOOKMARK_MODE"])
		Opt["BOOKMARK_MODE"] = 0
	if (Opt["BOOKMARK_MODE"] != 0 && Opt["BOOKMARK_MODE"] != 1)
		stop(1, "invalid bookmark mode: " Opt["BOOKMARK_MODE"])
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

function configure_origin_backup(	_i, _ds_suffix, _origin_arr, _src_origin, _origin_ds, _origin_snap,
					_target_origin, _origin_count) {
	if (!Opt["ORIGIN_ID"]) return
	if (!Origin["DS"])
		load_endpoint(Opt["ORIGIN_ID"], Origin)
	_target_origin = Origin["DS"]
	for (_i = 1; _i <= NumDS; _i++) {
		_ds_suffix  = DSList[_i]
		_src_origin = Dataset["SRC", _ds_suffix, "origin"]
		if (split(_src_origin, _origin_arr, "@") != 2) continue
		_origin_snap = "@" _origin_arr[2]
		if (DSPair[_ds_suffix, "match"]) continue
		DSPair[_ds_suffix, "source_origin_match"] = _src_origin
		DSPair[_ds_suffix, "target_origin"] = _target_origin
		DSPair[_ds_suffix, "match"] = _origin_snap
		DSPair[_ds_suffix, "source_start"] = _origin_snap
		DSPair[_ds_suffix, "source_end"] = Dataset["SRC", _ds_suffix, "latest_snapshot"]
		Action[_ds_suffix, "can_sync"] = 1
		Action[_ds_suffix, "status_notice"] = ""
		Action[_ds_suffix, "status_info"] = ""
		_origin_count++
	}
	if (!_origin_count)
		stop(1, "source has no clone origin: " Opt["SRC_ID"])
}

function clone_backup_needs_snapshot(	_origin_arr, _origin_snap) {
	if (split(Dataset["SRC", "", "origin"], _origin_arr, "@") != 2)
		return 0
	_origin_snap = "@" _origin_arr[2]
	return Dataset["SRC", "", "latest_snapshot"] == _origin_snap
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
																																					_clone_ds, _clone_count, _success_count, _cmd_status, _clone_action,
						_best_effort, _restored, _parent, _blocked, _failed_count, _skipped_count) {
	_remote			= Opt[endpoint"_REMOTE"]
	_user_snap		= Opt[endpoint"_SNAP"]
	_best_effort = (Opt["VERB"] == "revert" && Opt["REVERT_FORCE"])
	for (_i = 1; _i <= NumDS; _i++) {
		_ds_suffix		= DSList[_i]
		_idx			= endpoint SUBSEP _ds_suffix
		_last_snap		= Dataset[_idx, "latest_snapshot"]
		_snap			= _user_snap ? _user_snap : _last_snap
		_origin_ds_snap		= origin_ds _ds_suffix _snap
		_clone_ds		= new_ds _ds_suffix

		# Do not let clone -p invent empty parents for an unrestored branch.
		if (_best_effort) {
			_parent = _ds_suffix
			_blocked = 0
			while (sub(/\/[^\/]*$/, "", _parent)) {
				if (!(_parent in _restored)) {
					_blocked = 1
					break
				}
			}
			if (_blocked || !_last_snap) {
				_skipped_count++
				if (_blocked)
					report(LOG_ERROR, "skipping clone '" _clone_ds "': parent '" new_ds _parent "' was not restored")
				else
					report(LOG_ERROR, "skipping clone '" _clone_ds "': no source snapshot")
				continue
			}
		}
		if (!_last_snap) continue
		_clone_count++
		_cmd_arr["ds_snap"]	= rq(_remote, _origin_ds_snap)
		_cmd_arr["ds"]		= rq(_remote, _clone_ds)
		_cmd_arr["endpoint"]	= endpoint
		_cmd			= build_command("CLONE", _cmd_arr)
		_clone_action = Opt["DRYRUN"] ? "would clone: " : "cloning: "
		report(LOG_INFO, _clone_action _cmd_arr["ds_snap"])
		if (Opt["DRYRUN"]) {
			report(LOG_NOTICE, "+ " _cmd)
			_restored[_ds_suffix] = 1
			continue
		}
		report(LOG_DEBUG, "`"_cmd"`")
		# Some original-awk versions discard close(pipe)'s exit status.
		# Carry the clone status in-band so failures are portable.
		_cmd = "{ " _cmd CAPTURE_OUTPUT "; printf '\\nZELTA_CLONE_STATUS=%s\\n' \"$?\"; }"
		_cmd_status = 1
		FS="[[:space:]]"
		while (_cmd | getline) {
			if (/^ZELTA_CLONE_STATUS=[0-9]+$/)
				_cmd_status = substr($0, index($0, "=") + 1) + 0
			else if ($0 == "") continue
			else if (/encryption key not loaded/) report(LOG_INFO, "to mount " _clone_ds " load encryption key in " $NF)
			else report(LOG_WARNING, "unexpected 'zfs clone' output: " $0)
		}
		close(_cmd)
		if (_cmd_status) {
			if (!_best_effort)
				stop(1, "error creating clone '" _clone_ds "'")
			report(LOG_ERROR, "error creating clone '" _clone_ds "'")
			_failed_count++
			continue
		}
		_success_count++
		_restored[_ds_suffix] = 1
	}
	if (_clone_count)
		report(LOG_NOTICE, (Opt["DRYRUN"] ? "would clone " _clone_count : "cloned " _success_count) "/" NumDS " datasets to " new_ds)
	else
		report(LOG_NOTICE, "no source snapshots to clone")
	if (_best_effort && (_failed_count || _skipped_count)) {
		if (Opt["DRYRUN"])
			report(LOG_ERROR, "revert would be incomplete: " (_clone_count + 0) " planned, " (_skipped_count + 0) " skipped; original tree would be preserved at '" origin_ds "'")
		else
			report(LOG_ERROR, "revert incomplete: " (_success_count + 0) " cloned, " (_failed_count + 0) " failed, " (_skipped_count + 0) " skipped; original tree preserved at '" origin_ds "'")
		return _failed_count + _skipped_count
	}
	return 0
}

function run_clone_shuffle() {
	create_recursive_clone("SRC", Opt["SRC_DS"], Opt["TGT_DS"])
	Opt["VERB"] = "backup"
	load_opt_endpoint("SRC_", Target)
	load_opt_endpoint("TGT_", CloneBackupTarget)
	Opt["ORIGIN_ID"] = CloneOriginTarget["ID"]
	delete Source
	delete Target
	load_endpoint(Opt["SRC_ID"], Source)
	load_endpoint(Opt["TGT_ID"], Target)
	load_endpoint(Opt["ORIGIN_ID"], Origin)
	reset_runtime_state()
	validate_source_dataset()
	validate_target_dataset()
	validate_snapshots()
	if (clone_backup_needs_snapshot()) {
		create_source_snapshot(Opt["DRYRUN"] ? "would snapshot: " : "snapshotting: ")
	}
	compute_eligibility()
	configure_origin_backup()
	run_backup()
}

# A rename moves every descendant, even ones excluded from the clone plan.
# Validate the whole tree before moving anything out of its original path.
function validate_revert_tree(		_cmd_arr, _cmd, _line, _status, _invalid, _available, _datasets,
						_i, _ds, _snap, _selected) {
	_cmd_arr["endpoint"] = "SRC"
	_cmd_arr["ds"] = rq(Opt["SRC_REMOTE"], Opt["SRC_DS"])
	_cmd_arr["props"] = "name"
	_cmd_arr["flags"] = "-t filesystem,volume,snapshot"
	_cmd = build_command("LIST", _cmd_arr)
	report(LOG_DEBUG, "`" _cmd "`")
	# Original Awk may discard close(pipe)'s exit status. Require completion
	# in-band before trusting a listing that could have ended partway through.
	_cmd = "{ " _cmd CAPTURE_OUTPUT "; printf '\\nZELTA_REVERT_STATUS=%s\\n' \"$?\"; }"
	_status = 1
	while ((_cmd | getline _line) > 0) {
		if (_line ~ /^ZELTA_REVERT_STATUS=[0-9]+$/) {
			_status = substr(_line, index(_line, "=") + 1) + 0
			continue
		}
		if (_line == "") continue
		if (_line != Opt["SRC_DS"] && index(_line, Opt["SRC_DS"] "/") != 1 && index(_line, Opt["SRC_DS"] "@") != 1) {
			_invalid = 1
			continue
		}
		_available[_line] = 1
		if (!index(_line, "@")) _datasets[_line] = 1
	}
	close(_cmd)
	if (_status || _invalid || !_datasets[Opt["SRC_DS"]])
		stop(1, "cannot validate revert tree; no datasets renamed")

	for (_i = 1; _i <= NumDS; _i++) {
		_ds = Opt["SRC_DS"] DSList[_i]
		_selected[_ds] = 1
		_snap = Opt["SRC_SNAP"] ? Opt["SRC_SNAP"] : Dataset["SRC", DSList[_i], "latest_snapshot"]
		if (!_snap)
			stop(1, "cannot revert tree: no snapshot for '" _ds "'; revert eligible children individually")
		if (!((_ds _snap) in _available))
			stop(1, "cannot revert tree: missing snapshot '" _ds _snap "'; revert eligible children individually")
	}
	for (_ds in _datasets) {
		if (!(_ds in _selected))
			stop(1, "cannot revert tree: unselected dataset '" _ds "'; revert eligible children individually")
	}
}

function run_revert(		_ds, _followup) {
	if (Opt["REVERT_FORCE"])
		report(LOG_WARNING, "forcing best-effort revert; failed or skipped datasets remain in the preserved tree")
	else
		validate_revert_tree()
	# Disable snapshot
	# TO-DO: Add a mechanism to revert to the previous (rather than named) snapshot
	_ds = rename_dataset("SRC")
	if (create_recursive_clone("SRC", _ds, Opt["SRC_DS"])) {
		Summary["replicationErrorCode"] = 1
		return
	}
	create_source_snapshot(Opt["DRYRUN"] ? "would snapshot: " : "snapshotting: ")
	_followup = "zelta rotate '" Opt["SRC_DS"] "' 'TARGET'"
	if (Opt["DRYRUN"])
		report(LOG_NOTICE, "would recommend: " _followup)
	else
		report(LOG_NOTICE, "to retain replica history, run: " _followup)
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

function filtered_intermediate_mode() {
	return Opt["SEND_INTR"] && (Opt["EXCLUDE"] || Opt["INCLUDE"]) &&
	    (Opt["VERB"] != "replicate")
}

function load_send_range(ds_suffix, snap_arr,		_cmd, _base_name, _snap, _count, _prefix) {
	_base_name = Opt["SRC_DS"] ds_suffix
	_prefix = _base_name "@"
	_cmd = "ZELTA_DRYRUN='' zelta ipc-run prune --log-mode=text --log-level=2 --no-prune-guard --no-ranges --send-range=" q(DSPair[ds_suffix, "match"]) " " q(Source["ID"])
	report(LOG_DEBUG, "`"_cmd"`")
	_cmd = _cmd CAPTURE_OUTPUT
	while (_cmd | getline) {
		if (index($0, _prefix) == 1) {
			_snap = substr($0, length(_base_name) + 1)
			snap_arr[++_count] = _snap
		}
		else if (log_common_command_feedback() == LOG_ERROR)
			Summary["replicationErrorCode"] = 2
	}
	close(_cmd)
	return _count
}

function run_filtered_intermediate_backup(		_i, _s, _ds_suffix, _snap_num,
						_snap_arr, _last_snap, _syncing) {
	_syncing = Opt["DRYRUN"] ? "would sync " : "syncing "
	if (DSTree["syncable"])
		report(LOG_NOTICE, _syncing NumDS " datasets")

	for (_i = 1; _i <= NumDS; _i++) {
		_ds_suffix = DSList[_i]
		if (!Action[_ds_suffix, "can_sync"]) continue

		delete _snap_arr
		if (!DSPair[_ds_suffix, "match"]) {
			run_zfs_sync(_ds_suffix)
			if (Summary["replicationErrorCode"]) continue
			if (Opt["DRYRUN"])
				DSPair[_ds_suffix, "match"] = DSPair[_ds_suffix, "source_end"]
		}

		_snap_num = load_send_range(_ds_suffix, _snap_arr)
		_last_snap = DSPair[_ds_suffix, "match"]
		Action[_ds_suffix, "force_incremental"] = 1

		for (_s = 1; _s <= _snap_num; _s++) {
			DSPair[_ds_suffix, "source_start"] = _last_snap
			DSPair[_ds_suffix, "source_end"] = _snap_arr[_s]
			run_zfs_sync(_ds_suffix)
			if (Summary["replicationErrorCode"]) break
			_last_snap = _snap_arr[_s]
		}

		Action[_ds_suffix, "force_incremental"] = 0
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
	RunVerb = Opt["VERB"]

	# Validate arguments
	if (!is_null(Opt["DEPTH"]) && (Opt["DEPTH"] < 1))
		stop(1, "depth of '"Opt["DEPTH"]"' invalid; must be positive")
	validate_bookmark_mode()

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
	configure_origin_backup()
	run_send_check()

	if ((Opt["VERB"] == "clone") && (NumOperands == 4)) run_clone_shuffle()
	else if (Opt["VERB"] == "clone")	create_recursive_clone("SRC", Opt["SRC_DS"], Opt["TGT_DS"])
	else if (Opt["VERB"] == "revert")	run_revert()
	else if (Opt["VERB"] == "rotate")	run_rotate()
	else if (filtered_intermediate_mode())	run_filtered_intermediate_backup()
	else					run_backup()

	run_bookmark()

	Summary["endTime"]			= sys_time()
	Summary["runTime"]			= Summary["endTime"] - Summary["startTime"]

	compute_eligibility()
	print_summary()

	stop(Summary["replicationErrorCode"])
}
