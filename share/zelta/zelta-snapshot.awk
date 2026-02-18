#!/usr/bin/awk -f
#
# zelta-snapshot.awk
#
# Make a snapshot

function snapshot(	_snap_name, _ds_snap, _cmd_arr, _cmdk) {
	_snap_name = Opt["SRC_SNAP"] ? Opt["SRC_SNAP"] : get_snap_name()

	_ds_snap = Opt["SRC_DS"] _snap_name
	_cmd_arr["endpoint"] = "SRC"
	_cmd_arr["ds_snap"] = _ds_snap
	_cmd = build_command("SNAP", _cmd_arr)

	if (Opt["DRYRUN"]) {
		report(LOG_NOTICE, "+ "_cmd)
		stop()
	}

	report(LOG_DEBUG, "`"_cmd"`")
	if (system(_cmd))
		report(LOG_ERROR, "error creating '" _ds_snap "'")
	else
		report(LOG_NOTICE, "snapshot created '" _ds_snap "'")
}

BEGIN {
	snapshot()
	stop()
}
