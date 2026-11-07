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
	print "  -n, --dryrun               Show source/target table and exit"      > STDERR
	print "  -n -v, --dryrun --verbose  Show raw 'zelta backup' commands"       > STDERR
	print "  -H                         Scripting mode: no header, space-delimited" > STDERR
	print "  --snapshot                 Always snapshot"                        > STDERR
	print "  --no-snapshot              Never snapshot\n"                       > STDERR
	print "For complete documentation:  zelta help policy"                      > STDERR
	print "                             zelta help options"                     > STDERR
	print "                             https://zelta.space"                    > STDERR
	exit(1)
}

# Report policy parse errors with the original file, line number, and text.
function config_usage(line_num, message,		_line) {
	_line = ConfigLine[line_num]
	sub(/^[ \t]+/, "", _line)
	usage(message " at " ConfigFile[line_num] ":" ConfigFileLine[line_num] ": " _line)
}

# Return the parent directory for resolving local import fragments.
function file_dir(path,		_dir) {
	_dir = path
	if (_dir !~ /\//)
		return "."
	sub(/\/[^\/]*$/, "", _dir)
	return _dir ? _dir : "/"
}

# Resolve import paths relative to the file that references them.
function resolve_import(path, base_file,		_dir) {
	if (path ~ /^\//)
		return path
	_dir = file_dir(base_file)
	return _dir "/" path
}

# Expand simple textual import fragments before parsing the policy stream.
function load_config_lines(file, lines, files, file_lines, depth,		_arr, _base_indent, _child_file,
						_import_file, _line, _line_num, _raw, _sub_files,
						_sub_lines, _sub_line_nums, _sub_num, _i) {
	if (depth > 8)
		usage("import depth exceeded near " file)
	if (ImportStack[file])
		usage("recursive import detected: " file)

	ImportStack[file] = 1
	while ((getline _raw < file)>0) {
		_line_num++
		_line = _raw
		if (split(_line, _arr, "#"))
			_line = _arr[1]
		sub(/[ \t]+$/, "", _line)

		if (_line ~ /^[ ]*import:[[:space:]]+[^[:space:]]+/) {
			_base_indent = _line
			sub(/import:.*/, "", _base_indent)
			_import_file = _line
			sub(/^[ ]*import:[[:space:]]+/, "", _import_file)
			_child_file = resolve_import(_import_file, file)

			delete _sub_lines
			delete _sub_files
			delete _sub_line_nums
			_sub_num = load_config_lines(_child_file, _sub_lines, _sub_files, _sub_line_nums, depth + 1)
			for (_i = 1; _i <= _sub_num; _i++) {
				lines[++lines["count"]] = _sub_lines[_i] ? _base_indent _sub_lines[_i] : ""
				files[lines["count"]] = _sub_files[_i]
				file_lines[lines["count"]] = _sub_line_nums[_i]
			}
		} else {
			lines[++lines["count"]] = _raw
			files[lines["count"]] = file
			file_lines[lines["count"]] = _line_num
		}
	}
	close(file)
	delete ImportStack[file]
	if (!_line_num)
		usage("empty or unreadable policy file: " file)
	return lines["count"]
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

# Derive the hostname from a policy source key, which may include an SSH user.
function source_host(source_remote,		_host) {
	_host = source_remote
	sub(/^.*@/, "", _host)
	return _host
}

# Build the source endpoint passed to zelta backup.
function source_ep(job) {
	return job["source_remote"]":"job["source"]
}

# Generate the backup command string for a given job and options
function create_backup_command(job, opts,		_key, _cmd_prefix, _cmd_arr, _src, _tgt, _cmd) {
	for (_key in opts) {
		# Don't forward 'zelta policy' options
		if (PolicyOptScope[_key]) continue
		# TO-DO: Resolve flags for prettier commands?
		else if (opts[_key] != "")
			_cmd_prefix = str_add(_cmd_prefix, ENV_PREFIX _key "=" dq(opts[_key]))
	}
	# Construct command using command builder
	_src = q(source_ep(job))
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
	_conf_error = "configuration parse error"
	BACKUP_COMMAND = "zelta backup"

	_context = "global"
	Global["BACKUP_COMMAND"] = BACKUP_COMMAND

	NumConfigLines = load_config_lines(Opt["CONFIG"], ConfigLine, ConfigFile, ConfigFileLine, 0)
	for (_line_num = 1; _line_num <= NumConfigLines; _line_num++) {
		$0 = ConfigLine[_line_num]

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
			if (_context == "global") config_usage(_line_num, _conf_error)
			_context = "host"
			_job["source_remote"] = $2
			_job["host"] = source_host($2)
			arr_copy(_site_opt, _opt)
		} else if ($2 == "options") {
			_context = "options"
		} else if ($2 == "datasets") {
			_context = "datasets"
		} else if (/^      [^ ]+: +[^ ]/) {
			if (_context != "options") config_usage(_line_num, _conf_error)
			set_var(_opt, $2, $3)
		} else if (/^ +-[[:space:]]+[^ ]/) {
			if (!(_context ~ /^(datasets|host)$/)) config_usage(_line_num, _conf_error)
			_job["source"] = $3
			_job["target"] = resolve_target($4, _opt, _job)
			if (!_job["target"]) {
				report(LOG_WARNING,"no target defined for " _job["source"])
				continue
			}

			if (!should_backup(_job)) continue
			_opt["LOG_PREFIX"] = "[" _job["site"] ": " _job["target"] "] " source_ep(_job) ": "

			NumJobs++
			Job[NumJobs, "name"]      = "[" _job["site"] ": " _job["target"] "] " source_ep(_job)
			Job[NumJobs, "source_ep"] = source_ep(_job)
			Job[NumJobs, "target_ep"] = _job["target"]
			Job[NumJobs, "command"]   = create_backup_command(_job, _opt)
			# Track longest source endpoint for column alignment
			if (length(Job[NumJobs, "source_ep"]) > MaxSourceLen)
				MaxSourceLen = length(Job[NumJobs, "source_ep"])
		} else config_usage(_line_num, _conf_error)
	}
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
	_host_ep = source_ep(job)
	_leaf = job["source"]
	sub(/.*\//,"",_leaf)

	# Assemble possible match criteria; str_add() discards blank criteria
	_list = str_add(job["site"], job["host"], SUBSEP)
	_list = str_add(_list, job["source_remote"], SUBSEP)
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

# Print a tabular source/target listing for --dryrun (default, non-verbose)
function dryrun_table(		_j, _src, _tgt, _col, _line) {
	# Ensure header label fits in the column even on tiny configs
	_col = (MaxSourceLen > 6) ? MaxSourceLen : 6
	if (!Opt["NO_HEADER"])
		report(LOG_NOTICE, sprintf("%-*s  %s", _col, "SOURCE", "TARGET"))
	for (_j = 1; _j <= NumJobs; _j++) {
		_src = Job[_j, "source_ep"]
		_tgt = Job[_j, "target_ep"]
		if (Opt["NO_HEADER"])
			_line = _src " " _tgt
		else
			_line = sprintf("%-*s  %s", _col, _src, _tgt)
		report(LOG_NOTICE, _line)
	}
}

# Print raw zelta backup commands for --dryrun --verbose, stripping policy-internal vars.
# Policy sets ZELTA_LOG_PREFIX (which may contain spaces), LOG_MODE, LOG_LEVEL, and
# LOG_COMMAND on each child command. These are meaningless when running zelta backup
# standalone and clutter copy-pasteable output. Strip them via regex on the command string;
# each var is double-quoted so we match ZELTA_KEY="..." including any spaces in the value.
function dryrun_verbose(		_j, _cmd) {
	for (_j = 1; _j <= NumJobs; _j++) {
		_cmd = Job[_j, "command"]
		gsub(/ZELTA_LOG_PREFIX="[^"]*" /,  "", _cmd)
		gsub(/ZELTA_LOG_MODE="[^"]*" /,    "", _cmd)
		gsub(/ZELTA_LOG_LEVEL="[^"]*" /,   "", _cmd)
		gsub(/ZELTA_LOG_COMMAND="[^"]*" /, "", _cmd)
		report(LOG_NOTICE, "+ " _cmd)
	}
}

# Execute a single backup job and return success/failure
function zelta_backup(job_num,		_cmd, _return_code) {
	_cmd = Job[job_num, "command"]
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
	if (Opt["DRYRUN"]) {
		# --dryrun --verbose: raw commands with policy-internal vars stripped
		if (Opt["LOG_LEVEL"] >= LOG_INFO)
			dryrun_verbose()
		# --dryrun (default): clean source/target table
		else
			dryrun_table()
		stop(0)
	}
	if (should_xargs()) xargs()
	else backup_loop()
	stop(0)
}
