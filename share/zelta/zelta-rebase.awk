#!/usr/bin/env awk -f
#
# zelta-rebase.awk - rebase a dataset onto an upgraded upstream.

## Usage
########

function usage(message) {
	if (message)
		report(LOG_ERROR, message)
	print "usage: zelta rebase [OPTIONS] UPSTREAM TARGET" > STDERR
	print "" > STDERR
	print "Options:" > STDERR
	print "  -n, --dryrun               show commands but do not run them" > STDERR
	stop(1)
}

## Rebase
#########

function validate_rebase(upstream_ep, target_ep) {
	if (Opt["USAGE"])
		usage()
	if (Opt["REBASE_FILE"])
		stop(1, "--rebase-file is not supported by this rebase workflow")
	if (NumOperands != 2)
		usage("zelta rebase requires UPSTREAM and TARGET")
	load_endpoint(Operands[1], upstream_ep)
	load_endpoint(Operands[2], target_ep)
	if (upstream_ep["REMOTE"] != target_ep["REMOTE"])
		stop(1, "rebase requires UPSTREAM and TARGET on the same host")
}

function run_rebase_command(    _cmd_arr, _cmd, _error) {
	_cmd = build_command("ROTATE", _cmd_arr)
	if (Opt["DRYRUN"]) {
		report(LOG_NOTICE, "+ " _cmd)
		return
	}
	report(LOG_INFO, "+ " _cmd)
	report(LOG_DEBUG, "`" _cmd "`")
	_cmd = _cmd CAPTURE_OUTPUT
	while (_cmd | getline) {
		if ($0 ~ /^error:|^usage:/) {
			report(LOG_ERROR, $0)
			_error = 1
		} else if (log_common_command_feedback() == LOG_ERROR)
			_error = 1
	}
	close(_cmd)
	if (_error)
		stop(1, "rebase failed")
}

function run_rebase(    _upstream, _target) {
	validate_rebase(_upstream, _target)
	report(LOG_NOTICE, "rebasing " _target["ID"] " from " _upstream["ID"])
	run_rebase_command()
}

## Main
#######

BEGIN {
	FS = "\t"
	run_rebase()
	stop()
}
