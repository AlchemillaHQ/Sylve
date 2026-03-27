#!/usr/bin/awk -f
#
# zelta-args.awk: serialize common zelta arguments

# Choose a hostname for logging
function validate_host(host,		_hostname_cmd) {
	if (!host || (host == "localhost"))
		return Opt["HOSTNAME"]
	else
		return host
}

# TO-DO: Move to 'zelta-common.awk' as a generic endpoint calculator
# Load and parse two endpoints, the source and target, sequentially
# Create a set of variables from an scp-like host-dataset argument
# [[user@]host:]dataset[@snapshot]
function get_endpoint(		ep_type, _str_parts, _id, _remote ,_user, _host, _ds, _snap) {

	if (!NewOpt["SRC_ID"]) ep_type = "SRC_"
	else if (!NewOpt["TGT_ID"]) ep_type = "TGT_"
	else return

	_id = $0				# ID is the user's endpoint string

	# Find the connection info for ssh, '[user@]host'
	if (/^[^ :\/]+:/) {
		_remote = $0
		sub(/:.*/, "", _remote)		# REMOTE is '[user@]host'
		sub(/^[^ :\/]+:/,i "")		# Don't split(), $0 may have ':'
		if (split(_remote, _str_parts, "@")==2) {
			_user = _str_parts[1]	# USER from 'user@host'
			_host = _str_parts[2]	# HOST
		} else _host = _str_parts[1]	# HOST only
		# Special case: If the DS or SNAP contains a ':' and our target is local, we
		# have a work around: 'localhost:' _remote (with no user) gets cleared (so no ssh).
		if (!_user && (_host == "localhost")) _remote = ""
	}
	if (split($0, _str_parts, "@") == 2) {
		_snap = "@" _str_parts[2]
	}
	_ds = _str_parts[1]
	if (!_user) { _user = ENVIRON["USER"] }	# USER may be useful for logging

	# Validate and define the endpoint
	if (! _ds) stop(1, "invalid endpoint '"_id"'")
	NewOpt[ep_type "ID"]		= _id
	NewOpt[ep_type "REMOTE"]	= _remote
	NewOpt[ep_type "USER"]		= _user
	NewOpt[ep_type "HOST"]		= validate_host(_host)
	NewOpt[ep_type "DS"]		= _ds
	NewOpt[ep_type "SNAP"]		= _snap
}

function match_arg(arg, 	_flag) {
	for (_flag in OptListFlags) {
		if (arg == _flag) return OptListFlags[_flag]
	}
	stop(1, "invalid option '"arg"'")
}

function set_arg(flag, subopt,		_type, key) {
	_type = OptListType[flag]
	_key  = OptListKey[flag]
	if (_type == "arglist")       NewOpt[_key] = str_add(NewOpt[_key], $0)
	else if (_type == "list")     NewOpt[_key] = str_add(NewOpt[_key], subopt, ",")
	else if (_type == "true")     NewOpt[_key] = "1"
	else if (_type == "false")    NewOpt[_key] = "0"
	else if (_type == "set")      NewOpt[_key] = subopt
	else if (_type == "incr")     NewOpt[_key]++
	else if (_type == "decr")     NewOpt[_key]--
	else if (_type == "invalid")  stop(1, OptListWarn[flag])
	if (OptListWarn[flag])
		report(LOG_WARNING, OptListWarn[flag])
}

# Handle 'set' and 'list' action logic
function get_subopt(flag, m,	_subopt, _has_equals) {
	# Check if the original input had an '=' (for --key= or --key=value)
	_has_equals = index($0, "=")

	# If a key=value is given out of context, stop
	if (_has_equals && (!(OptListType[flag] in SUBOPT_TYPES) || OptListValue[flag])) {
		stop(1, "invalid option assignment '"$0"'")
	}
	# Not a subopt type
	else if (!(OptListType[flag] in SUBOPT_TYPES)) return ""
	# --key=value or --key= (empty value is valid)
	else if (_has_equals) return $2
	# Value is defined upstream
	else if (OptListValue[flag]) return OptListValue[flag]
	# Single dash option
	if (m) {
		_subopt = substr($0, m+1)
		# -k1
		if (_subopt) return _subopt
	}
	# Note, we're modifying ARGV's 'Idx' as a global because the logic to reconcile
	# this otherwise would be gnarly and inefficient.
	# Find '--key value' or '-k 1' (where value may be empty string)
	if (++Idx >= ARGC) {
		stop(1, "option '"$1"' requires an argument")
	}
	return ARGV[Idx]
}

function get_args(		_i, _flag, _arg, _m, _subopt, _opts_done) {
	FS = "="
	for (Idx = 1; Idx < ARGC; Idx++) {
		$0 = ARGV[Idx]

		if ($0 == "--") _opts_done++
		else if (_opts_done || /^[^-]/) {
			_opts_done++
			NewOpt["OPERANDS"] = str_add(NewOpt["OPERANDS"], $0, SUBSEP)
			get_endpoint()
			# Push to the list of operands
		}
		else if (/^--[^-]/) {
			_flag = match_arg($1)
			_subopt = get_subopt(_flag)
			set_arg(_flag, _subopt)
		}
		else if (/^-[^-]/) {
			# step through basic -opts
			for (_m=2; _m <= length($0); _m++) {
				_arg = "-" substr($0, _m, 1)
				_flag = match_arg(_arg)
				_subopt = get_subopt(_flag, _m)
				set_arg(_flag, _subopt)
				# If our _subopt was an argument, skip to the next word
				if (_subopt && !OptListValue[_flag]) break
			}
		} else stop(1, "invalid option: '"$0"'")
	}
}

# Load and index option file
function load_option_list(		_tsv, _flag, _flags, _idx, _flag_arr) {
	_tsv = Opt["SHARE"]"/zelta-opts.tsv"
	FS="\t"
	# TO-DO: Complain if TSV doesn't load
	while ((getline<_tsv)>0) {
		if (index($1, Opt["VERB"]) || ($1 == "all")) {
			# 1:VERBS 2:FLAGS 3:KEY 4:KEY_ALIAS 5:TYPE 6:VALUE 7:DESCRIPTION 8:WARNING
			if (/^#/ || !$2) continue
			_flags = $2
			split(_flags, _flag_arr, ",")
			# Make an dictionary for flag synonyms
			for (_idx in _flag_arr) OptListFlags[_flag_arr[_idx]] = _flags
			OptListKey[_flags]    = $3
			OptListType[_flags]   = $5
			OptListValue[_flags]  = $6
			OptListWarn[_flags]   = $8
			# We need to know the default of 'incr'/'decr' action items
			if ((OptListType[_flags] == "incr") || (OptListType[_flags] == "decr")) {
				_incr_decr_key = OptListKey[_flags]
				NewOpt[_incr_decr_key] = Opt[_incr_decr_key]
			}
		} else if (Opt[$4] && !Opt["LEGACY_ENV"]) {
			# Check for legacy variables and reassign them
			if ($8) report(LOG_WARNING, $8)
			NewOpt[$3] = Opt[$4]
			NewOpt["LEGACY_ENV"] = 1
		}


	}
	close(_tsv)
}

# Send an override back to 'zelta' when an arg has changed
function override_options(	_e) {
	for (_e in NewOpt) {
		if (NewOpt[_e] != Opt[_e]) {
			# TO-DO: Ensure numerical '0' exports properly
			export = export " " (ENV_PREFIX _e) "='" NewOpt[_e] "'"
		}
	}
	# This must be an explicit 'print' for shell input (not 'zelta ipc-log')
	if (export) print export
}

BEGIN {
	Opt["LOG_MODE"] = ""
	SUBOPT_TYPES["list"]  = 1
	SUBOPT_TYPES["set"]   = 1
	load_option_list()
	get_args()
	override_options()
	stop()
}
