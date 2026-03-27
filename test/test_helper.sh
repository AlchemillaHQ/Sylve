# shellcheck shell=sh

# Zelta Test Helper
#
# Environment variables:
#   SANDBOX_ZELTA_SRC_POOL    - Source pool
#   SANDBOX_ZELTA_TGT_POOL    - Target pool
#   SANDBOX_ZELTA_SRC_REMOTE  - Source [user@]host for remote tests
#   SANDBOX_ZELTA_TGT_REMOTE  - Target [user@]host for remote tests
#   SANDBOX_ZELTA_SRC_DS      - Source dataset
#   SANDBOX_ZELTA_TGT_DS      - Target dataset
#
# If pools/hosts are not configured, only basic sanity tests run.

## Setup temporary installation for testing
#############################################



setup_env() {
    export SANDBOX_ZELTA_TMP_DIR="/tmp/zelta$$"
    export SANDBOX_ZELTA_PROCNUM="$$"
    export ZELTA_BIN="$SANDBOX_ZELTA_TMP_DIR/bin"
    export ZELTA_SHARE="$SANDBOX_ZELTA_TMP_DIR/share"
    export ZELTA_ETC="$SANDBOX_ZELTA_TMP_DIR/etc"
    export ZELTA_DOC="$SANDBOX_ZELTA_TMP_DIR/man"
    export PATH="$ZELTA_BIN:$PATH"
}

build_endpoints() {
    ## Build endpoints
    if [ -n "$SANDBOX_ZELTA_SRC_REMOTE" ]; then
        SANDBOX_ZELTA_SRC_EP="${SANDBOX_ZELTA_SRC_REMOTE}:${SANDBOX_ZELTA_SRC_DS}"
    else
        SANDBOX_ZELTA_SRC_EP="$SANDBOX_ZELTA_SRC_DS"
    fi

    if [ -n "$SANDBOX_ZELTA_TGT_REMOTE" ]; then
        SANDBOX_ZELTA_TGT_EP="${SANDBOX_ZELTA_TGT_REMOTE}:${SANDBOX_ZELTA_TGT_DS}"
    else
        SANDBOX_ZELTA_TGT_EP="$SANDBOX_ZELTA_TGT_DS"
    fi

    export SANDBOX_ZELTA_SRC_POOL SANDBOX_ZELTA_TGT_POOL
    export SANDBOX_ZELTA_SRC_DS SANDBOX_ZELTA_TGT_DS
    export SANDBOX_ZELTA_SRC_EP SANDBOX_ZELTA_TGT_EP
}

# bypass using $$ if we've manually set these vars
if [ -z "$SANDBOX_ZELTA_TMP_DIR" ]; then
   setup_env
fi

build_endpoints

# We could use the repo dirs, but better to test installation
# use_repo_zelta() {
# 	REPO_ROOT="$SHELLSPEC_PROJECT_ROOT"
# 	export PATH="$REPO_ROOT/bin:$PATH"
# 	export ZELTA_SHARE="$REPO_ROOT/share/zelta"
# }



## Command execution wrappers
##############################

# Execute command on source (remote or local with sudo)
src_exec() {
	if [ -n "$SANDBOX_ZELTA_SRC_REMOTE" ]; then
		ssh -n "$SANDBOX_ZELTA_SRC_REMOTE" sudo "$@"
	else
		eval sudo "$@"
	fi
}

# Execute command on target (remote or local with sudo)
tgt_exec() {
	if [ -n "$SANDBOX_ZELTA_TGT_REMOTE" ]; then
		ssh -n "$SANDBOX_ZELTA_TGT_REMOTE" sudo "$@"
	else
		eval sudo "$@"
	fi
}


## Helpers
##########

check_install() {
    _installed=1
    if [ ! -x "$ZELTA_BIN/zelta" ]; then
        echo missing: "$ZELTA_BIN/zelta" >/dev/stderr
        _installed=0
    fi
    if [ ! -f "$ZELTA_SHARE/zelta-common.awk" ]; then
        echo missing: "$ZELTA_SHARE/zelta-common.awk" >/dev/stderr
        _installed=0
    fi
    if [ ! -f "$ZELTA_DOC/man8/zelta.8" ]; then
        echo missing: "$ZELTA_DOC/man8/zelta.8" >/dev/stderr
        _installed=0
    fi
    if [ ! -f "$ZELTA_ETC/zelta.conf.example" ]; then
        echo missing: "$ZELTA_ETC/zelta.conf.example" >/dev/stderr
        _installed=0
    fi
    [ $_installed = 1 ] && return 0
    return 1
}

# Make sure the installer worked and clean up carefully
cleanup_temp_install() {
    find "$SANDBOX_ZELTA_TMP_DIR" -type f | wc -w
	if [ -d "$SANDBOX_ZELTA_TMP_DIR" ]; then
		rm "$ZELTA_ETC"/zelta.*
		rmdir "$SANDBOX_ZELTA_TMP_DIR"/*
		rmdir "$SANDBOX_ZELTA_TMP_DIR"
    	[ ! -e "$SANDBOX_ZELTA_TMP_DIR" ] && return 0
	fi
	return 1
}

tmpfile_touch() {
    touch "${SHELLSPEC_TMPBASE}/${1}_${SANDBOX_ZELTA_PROCNUM}"
}

tmpfile_check() {
    [ ! -f "${SHELLSPEC_TMPBASE}/${1}_${SANDBOX_ZELTA_PROCNUM}" ]
}

skip_if_root() {
	[ "$(id -u)" -eq 0 ]
}

backup_no_op_check() {
    _options_all="-v --verbose -q --quiet --log-level=2 --log-mode=json --dryrun -n --depth 2 -d2 -X/sub3"
    _options_backup="--json -j --resume --snap-name=@test --snapshot --pull -i -o compression=zstd -x mountpoint"
    zelta backup $_options_all $_options_backup "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
}

backup_check_json() {
    backup_no_op_check 2>/dev/null | jq -re .output_version.command
}

# Check if source pool is a prefix of source dataset
src_pool_matches_ds() {
	case "$SANDBOX_ZELTA_SRC_DS" in
		"$SANDBOX_ZELTA_SRC_POOL"/*) return 0 ;;
		*) return 1 ;;
	esac
}

# Check if target pool is a prefix of target dataset
tgt_pool_matches_ds() {
	case "$SANDBOX_ZELTA_TGT_DS" in
		"$SANDBOX_ZELTA_TGT_POOL"/*) return 0 ;;
		*) return 1 ;;
	esac
}

# Check if source command with sudo works
src_cmd_works() {
	src_exec true
}

# Check if target command with sudo works
tgt_cmd_works() {
	tgt_exec true
}

# Skip if pools not configured (pure shell check, no external commands)
skip_pools() {
	[ -z "$SANDBOX_ZELTA_SRC_POOL" ] || [ -z "$SANDBOX_ZELTA_TGT_POOL" ]
}

skip_src_pool() {
	if [ -n "$SANDBOX_ZELTA_SRC_POOL" ] && src_pool_matches_ds; then
		return 1
	fi
    return 0
}

skip_tgt_pool() {
	if [ -n "$SANDBOX_ZELTA_TGT_POOL" ] && tgt_pool_matches_ds; then
		return 1
	fi
    return 0
}

nuke_pool() {
	_pool_name="$1"
	_exec_func="$2"
	_pool_file=/tmp/$_pool_name.img
	$_exec_func zpool destroy -f "$_pool_name" >/dev/null 2>&1
	$_exec_func rm -f "$_pool_file"
	return 0
}

make_pool() {
	_pool_name="$1"
	_exec_func="$2"
	_pool_file=/tmp/$_pool_name.img
	$_exec_func truncate -s 64m "$_pool_file"
	$_exec_func zpool create -f "$_pool_name" "$_pool_file"
	return $?
}

nuke_src_pool() {
	nuke_pool "$SANDBOX_ZELTA_SRC_POOL" src_exec
	return $?
}

nuke_tgt_pool() {
	nuke_pool "$SANDBOX_ZELTA_TGT_POOL" tgt_exec
	return $?
}

make_src_pool() {
	make_pool "$SANDBOX_ZELTA_SRC_POOL" src_exec || return 1
	tmpfile_touch "src_pool_created"

	# Grant ZFS permissions for source pool
	# original list snapshot,bookmark,send,hold
	ZFS_SRC_PERMS=snapshot,bookmark,send,hold,clone,create,mount,canmount,mountpoint,rename,readonly
	if [ -n "$SANDBOX_ZELTA_SRC_REMOTE" ]; then
		#ssh -n "$SANDBOX_ZELTA_SRC_REMOTE"
		src_exec "zfs allow -u \$USER $ZFS_SRC_PERMS $SANDBOX_ZELTA_SRC_POOL"
	else
		sudo zfs allow -u "$USER" $ZFS_SRC_PERMS "$SANDBOX_ZELTA_SRC_POOL"
	fi
	return $?
}

make_tgt_pool() {
	make_pool "$SANDBOX_ZELTA_TGT_POOL" tgt_exec || return 1
	tmpfile_touch "tgt_pool_created"
	
	# Grant ZFS permissions for target pool
	# original list receive,mount,create,canmount,rename
	ZFS_TGT_PERMS=receive,snapshot,bookmark,send,hold,clone,create,mount,canmount,mountpoint,rename,readonly
	if [ -n "$SANDBOX_ZELTA_TGT_REMOTE" ]; then
		#ssh -n "$SANDBOX_ZELTA_TGT_REMOTE" "zfs allow -u \$USER mount,create,rename $SANDBOX_ZELTA_TGT_POOL"
		tgt_exec "zfs allow -u \$USER $ZFS_TGT_PERMS $SANDBOX_ZELTA_TGT_POOL"
	else
		sudo zfs allow -u "$USER" $ZFS_TGT_PERMS "$SANDBOX_ZELTA_TGT_POOL"
	fi
	return $?
}

# Check if source dataset exists
src_ds_exists() {
	src_exec zfs list -H -o name "$SANDBOX_ZELTA_SRC_DS" >/dev/null 2>&1
	return $?
}

# Check if target dataset exists
tgt_ds_exists() {
	tgt_exec zfs list -H -o name "$SANDBOX_ZELTA_TGT_DS" >/dev/null 2>&1
	return $?
}

# Clean source dataset if it exists
clean_src_ds() {
	if src_ds_exists; then
	    src_exec rm -f /tmp/zfs_test_enc_key_${SANDBOX_ZELTA_PROCNUM}
		src_exec zfs destroy -r "$SANDBOX_ZELTA_SRC_DS"
		return $?
	fi
	return 0
}

# Clean target dataset if it exists
clean_tgt_ds() {
	if tgt_ds_exists; then
		tgt_exec zfs destroy -r "$SANDBOX_ZELTA_TGT_DS"
		return $?
	fi
	return 0
}

# Create divergent tree structure on source
# Creates a dataset tree with snapshots that will diverge from target

# Create divergent tree structure on source
# Creates a dataset tree with snapshots that will diverge from target
make_initial_tree() {
    if src_ds_exists; then
        echo "$SANDBOX_ZELTA_SRC_DS" already exists >/dev/stderr
        return 1
    fi
    if tgt_ds_exists; then
        echo "$SANDBOX_ZELTA_TGT_DS" already exists >/dev/stderr
        return 1
    fi

	# If we get this far, it will be safe to attempt to clean it up
	tmpfile_touch "divergent_tree_created"

	# Create encryption key
	src_exec dd if=/dev/urandom bs=32 count=1 of="/tmp/zfs_test_enc_key_${SANDBOX_ZELTA_PROCNUM}" >/dev/null 2>&1 || return 1


	# Create root dataset
	src_exec zfs create -u "$SANDBOX_ZELTA_SRC_DS" || return 1

	# Create child datasets
	src_exec zfs create -u "$SANDBOX_ZELTA_SRC_DS/sub1" || return 1
	src_exec zfs create -u "$SANDBOX_ZELTA_SRC_DS/sub2" || return 1
	src_exec zfs create -u "$SANDBOX_ZELTA_SRC_DS/sub2/orphan" || return 1
	src_exec zfs create -u "$SANDBOX_ZELTA_SRC_DS/sub3" || return 1
	src_exec zfs create -u "$SANDBOX_ZELTA_SRC_DS/sub3/space\ name" || return 1
	src_exec zfs create -u "$SANDBOX_ZELTA_SRC_DS/sub4" || return 1
	src_exec zfs create -sV 8M "$SANDBOX_ZELTA_SRC_DS/sub4/zvol" || return 1
	src_exec zfs create -u -o encryption=on -o keyformat=raw -o "keylocation=file:///tmp/zfs_test_enc_key_${SANDBOX_ZELTA_PROCNUM}" "$SANDBOX_ZELTA_SRC_DS/sub4/encrypted" || return 1

	return 0
}

# zelta backup moved to 022_setup_tree_spec as:
#        Divergent ree tests -> setup -> can zelta backup initial tree
# Replicate to target with @start snapshot
# zelta backup --snap-name @start "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP" || return 1

make_tree_divergence() {
	# Generate divergence
	src_exec zfs create -u "$SANDBOX_ZELTA_SRC_DS/sub1/child" || return 1
	tgt_exec zfs create -u "$SANDBOX_ZELTA_TGT_DS/sub1/kid" || return 1
	src_exec zfs destroy "$SANDBOX_ZELTA_SRC_DS/sub2@start" || return 1
	tgt_exec zfs snapshot "$SANDBOX_ZELTA_TGT_DS/sub3/space\ name@blocker" || return 1
	tgt_exec zfs destroy "$SANDBOX_ZELTA_TGT_DS/sub4/zvol@start" || return 1
	src_exec zfs snapshot "$SANDBOX_ZELTA_SRC_DS/sub3@two" || return 1
	src_exec zfs snapshot "$SANDBOX_ZELTA_SRC_DS/sub2@two" || return 1
	tgt_exec zfs snapshot "$SANDBOX_ZELTA_TGT_DS/sub2@two" || return 1

	return 0
}

add_tree_delta() {
	# make changes, we'll call this after snapshotting
	src_exec zfs create -u "$SANDBOX_ZELTA_SRC_DS/sub5" || return 1
	src_exec zfs create -u "$SANDBOX_ZELTA_SRC_DS/sub5/child1" || return 1

	return 0
}
