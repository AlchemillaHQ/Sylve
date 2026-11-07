#!/usr/bin/awk -f
#
# zelta-report.awk - report backup datasets with out of date snapshots.
#
# Note that this script has not been designed for public use. Contributions are welcome.

## Initialization
#################

function init_report() {
	# Calculate the age threshold (24 hours ago)
	TooOld = sys_time() - 86400
}

# Initialize state for processing a single endpoint
function init_endpoint(_endpoint_str) {
	# Clear previous endpoint state
	delete BackupRoot
	delete SeenDS
	delete OldList
	OutOfDateCount = 0
	UpToDateCount = 0

	# Parse the endpoint to handle remote targets
	load_endpoint(_endpoint_str, BackupRoot)
}

## ZFS List
###########

# Build and run the zfs list command for the backup root
function get_snapshot_ages(	_cmd, _remote) {
	_remote = get_remote_cmd(BackupRoot)
	_cmd = "zfs list -t filesystem,volume -Hpr -o name,snapshots_changed,usedbysnapshots -S snapshots_changed"
	_cmd = str_add(_cmd, qq(BackupRoot["DS"]))
	if (_remote) _cmd = _remote " " dq(_cmd)
	return _cmd
}

# Check for direct snapshots only when dataset properties are ambiguous
function has_snapshots(ds,	_cmd, _remote, _has_snapshots) {
	_remote = get_remote_cmd(BackupRoot)
	_cmd = "zfs list -Hro name -t snapshot -d 1 " qq(ds)
	if (_remote) _cmd = _remote " " dq(_cmd)
	_has_snapshots = ((_cmd | getline) > 0)
	close(_cmd)
	return _has_snapshots
}

# Parse zfs list output and categorize datasets
function parse_snapshot_list(	_cmd, _ds, _changed, _used_by_snapshots, _rel_name) {
	_cmd = get_snapshot_ages()
	FS = "[\t]+"
	while ((_cmd | getline) > 0) {
		_ds = $1
		_changed = $2
		_used_by_snapshots = $3
		# Skip if we've already seen this dataset
		if (_ds in SeenDS) continue
		SeenDS[_ds] = 1
		# Get relative name by removing backup root prefix
		_rel_name = _ds
		sub("^" BackupRoot["DS"] "/?", "", _rel_name)
		if (!_rel_name) _rel_name = BackupRoot["LEAF"]
		# Skip top-level dataset if it has no snapshots (that's OK)
		if (_rel_name == BackupRoot["LEAF"] && _changed == "-") continue
		# Skip datasets without snapshot info
		if (_changed == "-") continue
		# Categorize by age
		if (_changed < TooOld) {
			if (_used_by_snapshots == 0 && !has_snapshots(_ds)) {
				UpToDateCount++
				continue
			}
			OldList[++OutOfDateCount] = _rel_name
		} else {
			UpToDateCount++
		}
	}
	close(_cmd)
}

## Report Output
################

# Determine the report status for the endpoint.
function get_report_status() {
	if (!UpToDateCount && !OutOfDateCount)
		return "none"
	else if (!UpToDateCount)
		return "all"
	else if (OutOfDateCount > 0)
		return "some"
	return "ok"
}

# List out of date datasets as a single string for messages and commands.
function old_ds_list(	_i, _list) {
	for (_i = 1; _i <= OutOfDateCount; _i++)
		_list = str_add(_list, OldList[_i])
	return _list
}

# Replace literal tokens without AWK replacement-string interpretation.
function report_replace_all(str, token, value,	_out, _idx) {
	while ((_idx = index(str, token)) > 0) {
		_out = _out substr(str, 1, _idx - 1) value
		str = substr(str, _idx + length(token))
	}
	return _out str
}

# Substitute Zelta report symbols in user-defined message and command templates.
function expand_report_symbols(template, message, status,	_dslist) {
	_dslist = old_ds_list()
	template = report_replace_all(template, "{message}", message)
	template = report_replace_all(template, "{endpoint}", BackupRoot["ID"])
	template = report_replace_all(template, "{host}", BackupRoot["HOST"])
	template = report_replace_all(template, "{dataset}", BackupRoot["DS"])
	template = report_replace_all(template, "{dslist}", _dslist)
	template = report_replace_all(template, "{oldcount}", OutOfDateCount)
	template = report_replace_all(template, "{okcount}", UpToDateCount)
	template = report_replace_all(template, "{status}", status)
	return template
}

# Build the report message based on snapshot status.
function build_report_message(status,	_key, _msg) {
	_key = "REPORT_MESSAGE_" toupper(status)
	_msg = Opt[_key] ? Opt[_key] : Opt["REPORT_MESSAGE_DEFAULT"]
	if (!_msg) {
		if (status == "none")
			_msg = "{endpoint} no snapshots found"
		else if (status == "all")
			_msg = "{endpoint} all snapshots are out of date"
		else if (status == "some")
			_msg = "{endpoint} some snapshots are out of date: {dslist}"
		else
			_msg = "{endpoint} snapshots are up to date"
	}
	return expand_report_symbols(_msg, "", status)
}

# Run a user-configured report command, if any.
function run_report_command(status, message,	_key, _cmd) {
	_key = "REPORT_COMMAND_" toupper(status)
	_cmd = Opt[_key] ? Opt[_key] : Opt["REPORT_COMMAND_DEFAULT"]
	if (!_cmd) return
	_cmd = expand_report_symbols(_cmd, message, status)
	_cmd | getline
	close(_cmd)
}

# Process a single endpoint
function process_endpoint(_endpoint_str,	_status, _msg) {
	init_endpoint(_endpoint_str)
	parse_snapshot_list()
	_status = get_report_status()
	_msg = build_report_message(_status)
	report(LOG_NOTICE, _msg)
	run_report_command(_status, _msg)
}

## Main
#######

BEGIN {
	init_report()

	# Process BACKUP_ROOT if set, otherwise use operands
	if (Opt["BACKUP_ROOT"]) {
		process_endpoint(Opt["BACKUP_ROOT"])
	} else if (NumOperands >= 1) {
		for (_i = 1; _i <= NumOperands; _i++) {
			process_endpoint(Operands[_i])
		}
	} else {
		stop(1, "BACKUP_ROOT not set and no endpoints provided")
	}
}
