#!/usr/bin/env awk
#
# zelta-policy.awk, zelta policy - executes backup jobs from configuration files
#
# Implements policy-driven backup orchestration by parsing YAML-like configuration files,
# resolving backup targets, generating zelta backup commands, and executing jobs with
# retry logic and parallel execution support.
#
# CONCEPTS
# site: A logical grouping of hosts (e.g., "production", "development")
# host: A specific machine with datasets to backup
# job: A single backup operation from source to target
# target resolution: Computing backup destination paths from templates and job context
#
# GLOBALS
# Global: Policy-wide settings and overrides from config file
# Job: Array of backup jobs indexed by job number
# Sites: List of configured sites for parallel execution
# PolicyOpt: Valid policy option names for validation
# PolicyOptScope: Whether an option applies to policy vs backup commands

function usage(message) {
	if (message)
		print message > STDERR
	print "usage:  policy [backup-override-options] [site|host|dataset] ...\n"  > STDERR
	print "Runs replication jobs defined in: " Opt["CONFIG"] "\n"               > STDERR
	print "Without operands, run 'zelta backup' jobs for all configured"        > STDERR
	print "datasets. With operands, process the specified objects.\n"           > STDERR
	print "Common Options:"                                                     > STDERR
	print "  -v, -vv                    Verbose/debug output"                   > STDERR
	print "  -q, -qq                    Suppress warnings/errors"               > STDERR
	print "  -j, --json                 JSON output"                            > STDERR
	print "  -n, --dryrun               Show 'zelta backup' commands and exit"  > STDERR
	print "  --snapshot                 Always snapshot"                        > STDERR
	print "  --no-snapshot              Never snapshot\n"                       > STDERR
	print "For complete documentation:  zelta help policy"                      > STDERR
	print "                             zelta help options"                     > STDERR
	print "                             https://zelta.space"                    > STDERR
	exit(1)
}

# Resolve the backup target path based on options and job details
function resolve_target(tgt, opt, job,		_n, _i, _segments) {
	if (tgt) { return tgt }
	tgt = opt["BACKUP_ROOT"]
	if (opt["ADD_HOST_PREFIX"] && job["host"]) {
		tgt = tgt "/" job["host"]
	}
	_n = split(job["source"], _segments, "/")
	for (_i = _n - opt["ADD_DATASET_PREFIX"]; _i <= _n; _i++) {
		if (_segments[_i]) {
			tgt = tgt "/" _segments[_i]
		}
	}
	return tgt
}

# Generate the backup command string for a given job and options
function create_backup_command(job, opts,		_key, _cmd_prefix, _cmd_arr, _src, _tgt, _cmd) {
	for (_key in opts) {
		# Don't forward 'zelta policy' options
		if (PolicyOptScope[_key]) continue
		# TO-DO: Resolve flags for prettier commands?
		else if (opts[_key])
			_cmd_prefix = str_add(_cmd_prefix, ENV_PREFIX _key "=" dq(opts[_key]))
	}
	# Construct command using command builder
	_src = q(job["host"]":"job["source"])
	_tgt = q(job["target"])
	_cmd_arr["command_prefix"] = _cmd_prefix
	_cmd_arr["source"] = _src
	_cmd_arr["target"] = _tgt
	_cmd = build_command("BACKUP", _cmd_arr)

	return _cmd
}

# Set a variable in the option list, handling legacy mappings and validation
function set_var(option_list, var, val) {
	gsub(/-/,"_",var)
	gsub(/^ +/,"",var)
	gsub(/^ZELTA_/,"",var)
	var = toupper(var)
	if (var in PolicyLegacy) {
		# TO-DO: Legacy warning
		report(LOG_DEBUG, "reassigning the option '"var"' to '"PolicyLegacy[var]"'")
		var = PolicyLegacy[var]
	}
	if (!(var in PolicyOpt)) usage("unknown option: "var)
	if (var in Opt) {
		# Var is overriden by environment
		report(LOG_DEBUG, "skipping " var "; already in args/user env")
		return
	}
	# TO-DO: Validate type for boolean or number (incr/decr)
	# PolicyOptType
	if ((PolicyOptType[var] == "true") || (PolicyOptType[var] == "false")) {
		if (val in True)
			val = "1"
		else if (val in False)
			val = "0"
	} else if ((PolicyOptType[var] == "incr") || (PolicyOptType[var] == "decr")) {
		if (val !~ /^[0-9]$/)
			report(LOG_WARNING, "option '" var "' is an integer; '"val"' invalid")
		return
	}
	option_list[var] = val
}

# Load policy options from TSV file into global arrays for scope and type tracking
function load_option_list(	_tsv, _key, _idx, _flags, _flag_arr, _legacy_arr) {
	_tsv = Opt["SHARE"]"/zelta-opts.tsv"
	# TO-DO: Complain if TSV doesn't load
	FS="\t"
	while ((getline<_tsv)>0) {
		if (index($1, "policy") || ($1 == "all")) {
			# 1:VERBS 2:FLAGS 3:KEY 4:KEY_ALIAS 5:TYPE 6:VALUE 7:DESCRIPTION 8:WARNING
			if (/^#/ || !$3)
				continue
			_key = $3
			PolicyOptScope[_key]	= ($1 == "policy")
			PolicyOpt[_key]		= 1
			PolicyOptType[_key]	= $5
			PolicyOptWarn[_key]	= $8
			split($4, _legacy_arr, ",")
			for (_idx in _legacy_arr) PolicyLegacy[_legacy_arr[_idx]] = _key
		}
	}
	close(_tsv)
}

# Extract global overrides from options and restore logging settings
function get_global_overrides(		_key) {
	for (_key in Opt)
		if (PolicyOptScope[_key])
			Global[_key]	= Opt[_key]
	# The following are set because they're required for bootstrap,
	# so these are restored after the script starts.
	if (Opt["POLICY_LOG_MODE"])
		Opt["LOG_MODE"]		= Opt["POLICY_LOG_MODE"]
	if (Opt["POLICY_LOG_LEVEL"] != "")
		Opt["LOG_LEVEL"]	= Opt["POLICY_LOG_LEVEL"]
	if (Opt["POLICY_LOG_COMMAND"])
		Opt["LOG_COMMAND"]	= Opt["POLICY_LOG_COMMAND"]
	# Load Operands into an associative array for pattern matching
	create_assoc(Opt["OPERANDS"], Patterns, SUBSEP)
}

# Parse the configuration file to build job lists and options
function load_config(		_conf_error, _arr, _context, _job, _line_num,
				_site_opt, _opt, _legacy_arr) {
	# Split for YAML: Leading space, "- list item", "key: value", and "EOL:"
	FS = "^ +|- |:[[:space:]]+|:$"
	OFS=","
	_conf_error = "configuration parse error at line: "
	BACKUP_COMMAND = "zelta backup"

	_context = "global"
	Global["BACKUP_COMMAND"] = BACKUP_COMMAND

	while ((getline < Opt["CONFIG"])>0) {
		_line_num++

		# Clean up comments:
		if (split($0, _arr, "#")) {
			$0 = _arr[1]
		}
		sub(/[ \t]+$/, "")
		if (! $0) { continue }

		# Global options
		if (/^[^ ]+: +[^ ]/) {
			set_var(Global, $1, $2)

		# Sites:
		} else if (/^[^ ]+:$/) {
			_context = "site"
			_job["site"] = $1
			Sites[$1]++
			NumSites++
			arr_copy(Global, _site_opt)
		} else if (/^  [^ ]+: +[^ ]/) {
			set_var(_site_opt, $2, $3)

		# Hosts:
		} else if (/^  [^ ]+:$/) {
			if (_context == "global") usage(_conf_error _line_num)
			_context = "host"
			_job["host"] = $2
			arr_copy(_site_opt, _opt)
		} else if ($2 == "options") {
			_context = "options"
		} else if ($2 == "datasets") {
			_context = "datasets"
		} else if (/^      [^ ]+: +[^ ]/) {
			if (_context != "options") usage(_conf_error _line_num)
			set_var(_opt, $2, $3)
		} else if ((/^  - [^ ]/) || (/^    - [^ ]/)) {
			if (!(_context ~ /^(datasets|host)$/)) usage(_conf_error _line_num)
			_job["source"] = $3
			_job["target"] = resolve_target($4, _opt, _job)
			if (!_job["target"]) {
				report(LOG_WARNING,"no target defined for " _job["source"])
				continue
			}

			if (!should_backup(_job)) continue
			_opt["LOG_PREFIX"] = "[" _job["site"] ": " _job["target"] "] " _job["host"] ":" _job["source"]": "

			NumJobs++
			Job[NumJobs, "name"] = "[" _job["site"] ": " _job["target"] "] " _job["host"] ":" _job["source"]
			Job[NumJobs, "command"]      = create_backup_command(_job, _opt)
		} else usage(_conf_error _line_num)
	}
	close(Opt["CONFIG"])
	if (!NumJobs) {
		if (NumOperands)
			stop(1, "policy object(s) not found: " arr_join(Operands, ", "))
		else
			usage("no datasets defined in " Opt["CONFIG"])
	}
}

# Determine if xargs should be used for parallel execution
function should_xargs() {
	return ((Global["JOBS"] > 1) && (NumOperands > 1) && (NumSites > 1))
}

# Check if a job should be backed up based on operands/patterns
function should_backup(job,		_host_ep, _leaf, _list, _match_arr, _i) {
	if (!NumOperands) return 1
	_host_ep = job["host"]":"job["source"]
	_leaf = job["source"]
	sub(/.*\//,"",_leaf)

	# Assemble possible match criteria; str_add() discards blank criteria
	_list = str_add(job["site"], job["host"], SUBSEP)
	_list = str_add(_list, job["source"], SUBSEP)
	_list = str_add(_list, job["target"], SUBSEP)
	_list = str_add(_list, _host_ep, SUBSEP)
	_list = str_add(_list, _leaf, SUBSEP)
	create_assoc(_list, _match_arr, SUBSEP)

	# Match the operands to any of the above
	for (_i in Patterns)
		if (_i in _match_arr)
			return 1
	return 0
}

# Execute a single backup job and return success/failure
function zelta_backup(job_num,		_cmd, _return_code) {
	# Removed explicit output modes: LIST, ACTIVE, DEFAULT, VERBOSE
	# LIST is undocumented
	# ACTIVE creates a simplified indented print style
	_cmd = Job[job_num, "command"]
	if (Opt["DRYRUN"]) {
		report(LOG_NOTICE, "+ " _cmd)
		return 0
	}
	_return_code = system(_cmd)
	close(_cmd)
	# TO-DO: Use error codes to deduce if it seems to be retryable or not.
	return !!_return_code
}

# Run policy in parallel using xargs for multiple sites
function xargs(		_xargs_cmd, _site, _echo_sites, _return_code) {
	_policy_cmd = "zelta policy"
	_echo_sites = "echo"
	if (NumOperands)
		_echo_sites = str_add(_echo_sites, arr_join(Operands))
	else
		for (_site in Sites)
			_echo_sites = str_add(_echo_sites, q(_site))
	report(LOG_DEBUG, "launching " Global["JOBS"] " 'zelta policy' jobs")
	_xargs_cmd = _echo_sites " | xargs -n1 -P" Global["JOBS"] " " _policy_cmd
	system(_xargs_cmd)
	return _return_code
}

# Main loop to execute backup jobs with retry logic
function backup_loop(		_j, _num_failed, _failed_arr, _endpoint_key) {
	for (_j = 1; _j <= NumJobs; _j++) {
		if (zelta_backup(_j)) {
			_num_failed++
			_failed_arr[_j] = 1
		}
	}
	while ((Global["RETRY"]-- > 0) && _num_failed) {
		for (_endpoint_key in _failed_arr) {
			report(LOG_NOTICE, "retrying: " Job[_endpoint_key, "name"])
			if (!zelta_backup(_endpoint_key)) {
				delete _failed_arr[_endpoint_key]
				_num_failed--
			}
		}
	}
}

# Main entry point: initialize, load config, and execute jobs
BEGIN {
	if (Opt["USAGE"])
		usage()
	load_build_commands()
	load_option_list()
	get_global_overrides()
	load_config()
	if (should_xargs()) xargs()
	else backup_loop()
	stop(0)
}
