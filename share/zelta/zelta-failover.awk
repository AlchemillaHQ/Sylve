#!/usr/bin/awk -f
#
# zelta-failover.awk - lock, unlock, fail over, and sync properties for ZFS dataset trees.

## Command execution
####################

function run_cmd(cmd, error_code, error_msg,    _rc) {
	if (Opt["DRYRUN"])
		report(LOG_NOTICE, "+ " cmd)
	else
		report(LOG_INFO, "+ " cmd)
	if (Opt["DRYRUN"])
		return 0
	_rc = system(cmd)
	if (_rc) {
		if (!error_code)
			error_code = 1
		if (!error_msg)
			error_msg = "command failed"
		stop(error_code, error_msg ": " cmd)
	}
	return _rc
}

function try_cmd(cmd, error_msg, error_code,    _rc) {
	if (Opt["DRYRUN"])
		report(LOG_NOTICE, "+ " cmd)
	else
		report(LOG_INFO, "+ " cmd)
	if (Opt["DRYRUN"])
		return 0
	_rc = system(cmd)
	if (_rc) {
		if (!error_code)
			error_code = 1
		if (!error_msg)
			error_msg = "command failed"
		report(LOG_ERROR, error_msg ": " cmd)
		if (error_code > Summary["failoverErrorCode"])
			Summary["failoverErrorCode"] = error_code
	}
	return _rc
}

function endpoint_cmd(ep, cmd,    _remote) {
	_remote = get_remote_cmd(ep)
	if (_remote)
		return _remote " " dq(cmd)
	return cmd
}

function endpoint_build_cmd(ep, action, vars,    _cmd) {
	_cmd = build_command(action, vars)
	return endpoint_cmd(ep, _cmd)
}

function unmount_flags() {
	if (Opt["FORCE_UNMOUNT"])
		return "-f"
	return ""
}

function should_unmount() {
	return !Opt["NO_UNMOUNT"]
}

function unmount_command() {
	return str_add("zfs unmount", unmount_flags())
}

## Lock and unlock
##################

function lock_dataset(ep,    _cmd, _cmd_arr, _list_cmd, _mount_cmd, _list_local, _mount_local, _ds, _mounted) {
	_cmd_arr["ds"] = q(ep["DS"])
	_cmd_arr["props"] = "name"
	_cmd_arr["flags"] = "-t filesystem -Screatetxg"
	_list_local = build_command("LIST", _cmd_arr)
	_cmd_arr["props"] = "name,mounted"
	_mount_local = build_command("LIST", _cmd_arr)
	_list_cmd = endpoint_cmd(ep, _list_local)
	_mount_cmd = endpoint_cmd(ep, _mount_local)
	if (Opt["DRYRUN"]) {
		delete _cmd_arr
		_cmd_arr["prop"] = "readonly=on"
		_cmd_arr["ds"] = q(ep["DS"])
		report(LOG_NOTICE, "+ " endpoint_build_cmd(ep, "SET", _cmd_arr))
		report(LOG_NOTICE, "+ " endpoint_cmd(ep, _list_local " | xargs -n1 zfs set canmount=noauto"))
		if (should_unmount())
			report(LOG_NOTICE, "+ " endpoint_cmd(ep, _mount_local " | while read ds mounted; do [ \\\"$mounted\\\" = yes ] && " unmount_command() " \\\"$ds\\\"; done"))
		return
	}
	delete _cmd_arr
	_cmd_arr["prop"] = "readonly=on"
	_cmd_arr["ds"] = q(ep["DS"])
	run_cmd(endpoint_build_cmd(ep, "SET", _cmd_arr), 255, "failed to set readonly; dataset is not safely locked")
	_cmd = _list_cmd
	while ((_cmd | getline) > 0) {
		delete _cmd_arr
		_cmd_arr["prop"] = "canmount=noauto"
		_cmd_arr["ds"] = q($1)
		run_cmd(endpoint_build_cmd(ep, "SET", _cmd_arr))
	}
	close(_cmd)
	if (!should_unmount())
		return
	_cmd = _mount_cmd
	while ((_cmd | getline) > 0) {
		_ds = $1
		_mounted = $2
		if (_mounted == "yes") {
			delete _cmd_arr
			_cmd_arr["flags"] = unmount_flags()
			_cmd_arr["ds"] = q(_ds)
			try_cmd(endpoint_build_cmd(ep, "UNMOUNT", _cmd_arr), "failed to unmount locked dataset")
		}
	}
	close(_cmd)
}

function unlock_dataset(ep,    _cmd, _cmd_arr, _list_cmd, _mount_cmd, _list_local, _mount_local, _ds, _canmount, _mounted) {
	_cmd_arr["ds"] = q(ep["DS"])
	_cmd_arr["props"] = "name"
	_cmd_arr["flags"] = "-t filesystem -s createtxg"
	_list_local = build_command("LIST", _cmd_arr)
	_cmd_arr["props"] = "name,canmount,mounted"
	_mount_local = build_command("LIST", _cmd_arr)
	_list_cmd = endpoint_cmd(ep, _list_local)
	_mount_cmd = endpoint_cmd(ep, _mount_local)
	delete _cmd_arr
	_cmd_arr["prop"] = "readonly"
	_cmd_arr["ds"] = q(ep["DS"])
	run_cmd(endpoint_build_cmd(ep, "INHERIT", _cmd_arr), 255, "failed to clear readonly; dataset is not safely unlocked")
	if (Opt["DRYRUN"])
		report(LOG_NOTICE, "+ " endpoint_cmd(ep, _list_local " | xargs -n1 zfs set canmount=on"))
	else {
		_cmd = _list_cmd
		while ((_cmd | getline) > 0) {
			delete _cmd_arr
			_cmd_arr["prop"] = "canmount=on"
			_cmd_arr["ds"] = q($1)
			run_cmd(endpoint_build_cmd(ep, "SET", _cmd_arr))
		}
		close(_cmd)
	}
	delete _cmd_arr
	_cmd_arr["flags"] = "-R"
	_cmd_arr["ds"] = q(ep["DS"])
	try_cmd(endpoint_build_cmd(ep, "MOUNT", _cmd_arr), "failed recursive mount during unlock")
	if (Opt["DRYRUN"]) {
		report(LOG_NOTICE, "+ " endpoint_cmd(ep, _mount_local " | while read ds canmount mounted; do [ \\\"$canmount\\\" != off ] && [ \\\"$mounted\\\" != yes ] && zfs mount \\\"$ds\\\"; done"))
		return
	}
	_cmd = _mount_cmd
	while ((_cmd | getline) > 0) {
		_ds = $1
		_canmount = $2
		_mounted = $3
		if (_canmount != "off" && _mounted != "yes") {
			delete _cmd_arr
			_cmd_arr["ds"] = q(_ds)
			try_cmd(endpoint_build_cmd(ep, "MOUNT", _cmd_arr), "failed to mount unlocked dataset")
		}
	}
	close(_cmd)
}



## Property playback
####################

function rel_suffix(root, ds) {
	if (ds == root)
		return ""
	if (substr(ds, 1, length(root) + 1) != root "/")
		stop(1, "dataset is outside expected root: " ds)
	return substr(ds, length(root) + 1)
}

function load_local_props(ep, props, seen_ds,    _cmd, _cmd_arr, _ds, _prop, _val, _suffix) {
	_cmd_arr["flags"] = "-s local"
	_cmd_arr["props"] = "all"
	_cmd_arr["ds"] = q(ep["DS"])
	_cmd = endpoint_build_cmd(ep, "PROPS", _cmd_arr)
	while ((_cmd | getline) > 0) {
		_ds = $1
		_prop = $2
		_val = $3
		if (_prop == "volsize")
			continue
		if (_prop == "canmount")
			continue
		_suffix = rel_suffix(ep["DS"], _ds)
		if (_prop == "readonly" && _suffix == "")
			continue
		seen_ds[_suffix] = 1
		props[_suffix, _prop] = _val
	}
	close(_cmd)
}

function playback_source_props(tgt_ep,    _key, _parts, _suffix, _prop, _ds, _cmd_arr) {
	for (_key in SourceProp) {
		split(_key, _parts, SUBSEP)
		_suffix = _parts[1]
		_prop = _parts[2]
		_ds = tgt_ep["DS"] _suffix
		delete _cmd_arr
		_cmd_arr["prop"] = _prop "=" q(SourceProp[_key])
		_cmd_arr["ds"] = q(_ds)
		run_cmd(endpoint_build_cmd(tgt_ep, "SET", _cmd_arr))
	}
}

function inherit_target_only_props(tgt_ep,    _key, _parts, _suffix, _prop, _ds, _cmd_arr) {
	for (_key in TargetProp) {
		if (_key in SourceProp)
			continue
		split(_key, _parts, SUBSEP)
		_suffix = _parts[1]
		_prop = _parts[2]
		_ds = tgt_ep["DS"] _suffix
		delete _cmd_arr
		_cmd_arr["prop"] = _prop
		_cmd_arr["ds"] = q(_ds)
		run_cmd(endpoint_build_cmd(tgt_ep, "INHERIT", _cmd_arr))
	}
}

function sync_locked_source(src_ep, tgt_ep,    _cmd_arr, _cmd) {
	_cmd_arr["flags"] = "--snapshot --log-mode=text --log-level=" Opt["LOG_LEVEL"]
	_cmd_arr["source"] = q(src_ep["ID"])
	_cmd_arr["target"] = q(tgt_ep["ID"])
	_cmd = build_command("BACKUP_IPC", _cmd_arr)
	run_cmd(_cmd, 2, "final backup failed after source lock")
}

function load_prop_endpoints(src_ep, tgt_ep) {
	if (NumOperands != 2)
		stop(1, "zelta " Opt["VERB"] " requires SOURCE and TARGET")

	load_endpoint(Operands[1], src_ep)
	load_endpoint(Operands[2], tgt_ep)
	load_local_props(src_ep, SourceProp, SourceDS)
	load_local_props(tgt_ep, TargetProp, TargetDS)
}

function sync_props(tgt_ep) {
	playback_source_props(tgt_ep)
	inherit_target_only_props(tgt_ep)
}

## Failover
###########

function run_failover(    _src, _tgt) {
	if (NumOperands != 2)
		stop(1, "zelta failover requires SOURCE and TARGET")

	load_endpoint(Operands[1], _src)
	load_endpoint(Operands[2], _tgt)

	if (!Opt["DRYRUN"]) {
		load_local_props(_src, SourceProp, SourceDS)
		load_local_props(_tgt, TargetProp, TargetDS)
	}

	lock_dataset(_src)
	sync_locked_source(_src, _tgt)
	sync_props(_tgt)
	unlock_dataset(_tgt)
}

function run_propsync(    _src, _tgt) {
	load_prop_endpoints(_src, _tgt)
	sync_props(_tgt)
}

## Main
#######

BEGIN {
	FS = "\t"
	if (Opt["VERB"] == "lock" || Opt["VERB"] == "unlock") {
		if (NumOperands < 1)
			stop(1, "zelta " Opt["VERB"] " requires at least one dataset")
		for (_i = 1; _i <= NumOperands; _i++) {
			load_endpoint(Operands[_i], _ep)
			if (Opt["VERB"] == "unlock")
				unlock_dataset(_ep)
			else
				lock_dataset(_ep)
			delete _ep
		}
	} else if (Opt["VERB"] == "failover") {
		if (NumOperands != 2)
			stop(1, "zelta failover requires SOURCE and TARGET")
		run_failover()
	} else if (Opt["VERB"] == "propsync") {
		if (NumOperands != 2)
			stop(1, "zelta propsync requires SOURCE and TARGET")
		run_propsync()
	} else
		stop(1, "unsupported failover verb: " Opt["VERB"])
	stop(Summary["failoverErrorCode"])
}
