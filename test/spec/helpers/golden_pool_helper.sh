: "${SANDBOX_ZELTA_GOLD_PRUNE_SCEN_DIR:=${SHELLSPEC_HELPERDIR}/golden/0200_prune}"  # read-only truth: <pool>.img

_gp_log()  { printf '%s  %s\n' "$(date '+%H:%M:%S')" "$*"; }
_gp_warn() { printf '%s  WARN: %s\n' "$(date '+%H:%M:%S')" "$*" >&2; }
_gp_die()  { printf '%s  FATAL: %s\n' "$(date '+%H:%M:%S')" "$*" >&2; exit 1; }

_gp_validate_environment() {
    #[ "$(id -u)" -eq 0 ] || _gp_die "must run as root"
    command -v zpool >/dev/null 2>&1 || _gp_die "zpool not found"
}
_gp_pool_img_file() {
    printf "/tmp/%s.img" "$1"
}

_gp_golden_check_filename() {
    printf "golden_%s" "$1"
}

# Destroy one pool if imported, with a vdev-path safety check so a real
# same-named pool isn't clobbered. No-op if the pool isn't imported.
_gp_teardown_pool() {
	_pool_name="$1"
	_exec_func="$2"

    _pool_file=$(_gp_pool_img_file "$_pool_name")

	if ! $_exec_func zpool list "$_pool_name" >/dev/null 2>&1; then
		_gp_log "$_pool_name not imported (nothing to destroy)"
		return 0
	fi

    check_filename=$(_gp_golden_check_filename "$_pool_name")
    ! tmpfile_check "$check_filename" || _gp_die "teardown: guard for golden pool $_pool_name not found: $check_filename"

	if $_exec_func zpool status -P "$_pool_name" 2>/dev/null | grep -qF -- "$_pool_file"; then
		_gp_log "destroying $_pool_name (vdev: $_pool_file)"
		$_exec_func zpool destroy -f "$_pool_name" || $_exec_func zpool export "$_pool_name" \
			|| _gp_warn "could not destroy or export $_pool_name"
	elif [ "$FORCE" -eq 1 ]; then
		_gp_warn "$_pool_name vdev does not match $_pool_file; FORCE=1 -> destroying anyway"
		$_exec_func zpool destroy -f "$_pool_name" || $_exec_func zpool export "$_pool_name" \
			|| _gp_warn "could not destroy or export $_pool_name"
	else
		_gp_warn "$_pool_name is imported but its vdev is not $_pool_file; refusing (set FORCE=1 to override)"
	fi

	tmpfile_remove "$check_filename"
}

_gp_remove_image() {
	_exec_func=$1
	_pool_name=$2
	_remote=$3

	_pool_file=$(_gp_pool_img_file "$_pool_name")

	_gp_log "_gp_remove_image: pool file is $_pool_file"

    status=0
    if [ -n "$_remote" ]; then
        ssh "$_remote" test -e "$_pool_file" || status=$?
    else
        test -e "$_pool_file" || status=$?
    fi

	if [ "$status" -eq 0 ]; then
		$_exec_func rm -f "$_pool_file" && _gp_log "removed image $_pool_file" || _gp_warn "could not remove $_pool_file"
	else
		_gp_log "image $_pool_file absent (nothing to remove)"
	fi
}

# Tear down a prior import of this pool, but only if its vdev is the working
# copy — never clobber a real same-named pool. Idempotent.
_gp_pool_validate_destroy() {
    _exec_func=$1
	_pool_name=$2
    _pool_file=$3

    _gp_log "pool_name is $_pool_name"
    _gp_log "pool_file is $_pool_file"

	if ! $_exec_func zpool list "$_pool_name" >/dev/null 2>&1; then
		_gp_log "validate_destroy: $_pool_name not imported (nothing to cleanup)"
		return 0
	fi

	if $_exec_func zpool status -P "$_pool_name" 2>/dev/null | grep -qF -- "$_pool_file"; then
        check_filename=$(_gp_golden_check_filename "$_pool_name")
        ! tmpfile_check "$check_filename" || _gp_die "validate_destroy: guard for golden pool $_pool_name not found: $check_filename"
		$_exec_func zpool destroy -f "$_pool_name" || $_exec_func zpool export "$_pool_name" \
			|| _gp_die "could not clear prior import of $_pool_name"
	else
		_gp_die "$_pool_name is already imported on a vdev that is not $_pool_file; refusing"
	fi
}

make_golden_pool() {
	_exec_func=$1
	_pool_name=$2
    _remote=$3

    _golden_img="${SANDBOX_ZELTA_GOLD_PRUNE_SCEN_DIR}/${_pool_name}.img"

	[ -f "$_golden_img" ] || _gp_die "golden image not found: $_golden_img"

	_pool_file=$(_gp_pool_img_file "$_pool_name")
	_gp_log "make_golden_pool: pool file is $_pool_file"
    _gp_log "make_golden_pool: pool name is $_pool_name"
	_gp_pool_validate_destroy "$_exec_func" "$_pool_name" "$_pool_file"
    _gp_remove_image "$_exec_func" "$_pool_name" "$_remote"

    if [ -n "$_remote" ]; then
		scp "$_golden_img" "${_remote}:${_pool_file}" || _gp_die "scp copy $_golden_img to ${_remote}:${_pool_file} failed!"
    else
		cp "$_golden_img" "${_pool_file}" || _gp_die "copy $_golden_img to ${_pool_file} failed!"
    fi

	$_exec_func zpool import -d "$_pool_file" -f "$_pool_name" \
    		|| _gp_die "import of $_pool_name from $_pool_file failed"

    check_filename=$(_gp_golden_check_filename "$_pool_name")
    tmpfile_touch "$check_filename"
    _gp_log "make_golden: $_pool_name created from golden $_golden_img"
	return $?
}

teardown_golden_pools() {
    _gp_validate_environment

    _gp_teardown_pool "$SANDBOX_ZELTA_SRC_POOL" src_exec
    _gp_teardown_pool "$SANDBOX_ZELTA_TGT_POOL" tgt_exec

    _gp_remove_image src_exec "$SANDBOX_ZELTA_SRC_POOL" "$SANDBOX_ZELTA_SRC_REMOTE"
    _gp_remove_image tgt_exec "$SANDBOX_ZELTA_TGT_POOL" "$SANDBOX_ZELTA_TGT_REMOTE"

    _gp_log "reset complete."
}

make_golden_pools() {
  	make_golden_pool src_exec "$SANDBOX_ZELTA_SRC_POOL" "$SANDBOX_ZELTA_SRC_REMOTE" || return 1
  	make_golden_pool tgt_exec "$SANDBOX_ZELTA_TGT_POOL" "$SANDBOX_ZELTA_TGT_REMOTE" || return 1
}