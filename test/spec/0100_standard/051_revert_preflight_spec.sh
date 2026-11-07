# Exercise the real CLI with a fake ZFS tree; never touch a host's pools.
run_revert_preflight() {
	_case=$1
	shift
	_fakebin="$SHELLSPEC_TMPBASE/revert-preflight-bin"
	mkdir -p "$_fakebin"
	cat > "$_fakebin/zfs" <<'EOF'
#!/bin/sh
_command=$1
shift
case $_command in
get)
	for _ds in apool/source 'apool/source/child with space'; do
		printf '%s\ttype\tfilesystem\n' "$_ds"
		printf '%s\treadonly\toff\n' "$_ds"
	done
	case $REVERT_CASE in
	best-effort*|root-failure)
		for _ds in 'apool/source/child with space/dependent' 'apool/source/child with space2' apool/source/zhealthy; do
			printf '%s\ttype\tfilesystem\n' "$_ds"
			printf '%s\treadonly\toff\n' "$_ds"
		done
		;;
	esac
	;;
list)
	_props=
	_prev=
	for _arg do
		[ "$_prev" != -o ] || _props=$_arg
		_prev=$_arg
	done
	if [ "$_props" = name ]; then
		[ "$REVERT_CASE" != list-failure ] || exit 1
		printf '%s\n' apool/source apool/source@old 'apool/source/child with space'
		case $REVERT_CASE in
		coherent*) printf '%s\n' 'apool/source/child with space@old' ;;
		no-snapshot*) ;;
		*) printf '%s\n' 'apool/source/child with space@new' ;;
		esac
		[ "$REVERT_CASE" != coherent-list-failure ] || exit 23
	else
		printf 'apool/source@old\t2\n'
		printf 'apool/source\t1\n'
		case $REVERT_CASE in
		coherent*) printf 'apool/source/child with space@old\t4\n' ;;
		no-snapshot*) ;;
		*) printf 'apool/source/child with space@new\t5\n' ;;
		esac
		printf 'apool/source/child with space\t3\n'
		case $REVERT_CASE in
		best-effort*|root-failure)
			printf 'apool/source/child with space/dependent@old\t6\napool/source/child with space/dependent\t7\n'
			printf 'apool/source/child with space2@old\t8\napool/source/child with space2\t9\n'
			printf 'apool/source/zhealthy@old\t10\napool/source/zhealthy\t11\n'
			;;
		esac
	fi
	;;
rename|clone|snapshot)
	printf '%s\n' "$_command $*" >> "$REVERT_MUTATIONS"
	if [ "$REVERT_CASE" = root-failure ] && [ "$_command" = clone ]; then
		printf '%s\n' 'cannot clone root: permission denied' >&2
		exit 1
	fi
	case "$_command $*" in
	*'child with space@old'*)
		case $REVERT_CASE in coherent*) exit 0 ;; esac
		printf '%s\n' 'cannot open snapshot: dataset does not exist' >&2
		exit 1
		;;
	esac
	;;
*) exit 1 ;;
esac
EOF
	chmod +x "$_fakebin/zfs"
	_repo="${REPO_ROOT:-$SHELLSPEC_PROJECT_ROOT}"
	env PATH="$_fakebin:$_repo/bin:$PATH" ZELTA_SHARE="$_repo/share/zelta" \
		ZELTA_ENV=/dev/null REVERT_CASE="$_case" \
		REVERT_MUTATIONS="$SHELLSPEC_TMPBASE/revert-mutations-$_case" "$@"
}

# Expose the actual mutation sequence so tests can check continued siblings,
# skipped dependents, and the absence of a final snapshot on incomplete reverts.
run_best_effort_revert() {
	_fixture=$1
	_force=$2
	if run_revert_preflight "$_fixture" zelta revert -qq "$_force" apool/source@old; then
		_result=0
	else
		_result=$?
	fi
	cat "$SHELLSPEC_TMPBASE/revert-mutations-$_fixture"
	return "$_result"
}

Describe 'Revert whole-tree preflight' standard:51
	It 'rejects a missing child snapshot before any mutation'
		When call run_revert_preflight missing zelta revert -qq apool/source@old
		The status should be failure
		The error should eq "error: cannot revert tree: missing snapshot 'apool/source/child with space@old'; revert eligible children individually"
		The path "$SHELLSPEC_TMPBASE/revert-mutations-missing" should not be exist
	End

	It 'rejects children with no snapshots before any mutation'
		When call run_revert_preflight no-snapshot zelta revert -qq apool/source@old
		The status should be failure
		The error should include 'cannot revert tree:'
		The path "$SHELLSPEC_TMPBASE/revert-mutations-no-snapshot" should not be exist
	End

	It 'also rejects incomplete dry-run plans'
		When call run_revert_preflight dryrun zelta revert -qqn apool/source@old
		The status should be failure
		The error should include 'missing snapshot'
		The path "$SHELLSPEC_TMPBASE/revert-mutations-dryrun" should not be exist
	End

	It 'fails closed if the whole-tree listing fails'
		When call run_revert_preflight list-failure zelta revert -qq apool/source@old
		The status should be failure
		The error should eq 'error: cannot validate revert tree; no datasets renamed'
		The path "$SHELLSPEC_TMPBASE/revert-mutations-list-failure" should not be exist
	End

	Describe 'Failed listing with partial output'
		Parameters
			-qq
			-qqn
		End
		It "rejects valid-looking partial output with $1"
			When call run_revert_preflight coherent-list-failure zelta revert "$1" apool/source@old
			The status should be failure
			The error should eq 'error: cannot validate revert tree; no datasets renamed'
			The path "$SHELLSPEC_TMPBASE/revert-mutations-coherent-list-failure" should not be exist
		End
	End

	It 'permits a coherent recursive dry run'
		When call run_revert_preflight coherent zelta revert -qqn apool/source@old
		The status should be success
		The path "$SHELLSPEC_TMPBASE/revert-mutations-coherent" should not be exist
	End

	It 'rejects a depth limit that would strand children under the preserved path'
		When call run_revert_preflight coherent zelta revert -qqn -d 1 apool/source@old
		The status should be failure
		The error should include 'unselected dataset'
		The path "$SHELLSPEC_TMPBASE/revert-mutations-coherent" should not be exist
	End

	It 'rejects a child without snapshots when no snapshot name is given'
		When call run_revert_preflight no-snapshot zelta revert -qq apool/source
		The status should be failure
		The error should include 'no snapshot for'
		The path "$SHELLSPEC_TMPBASE/revert-mutations-no-snapshot" should not be exist
	End

	It 'rejects a filter that would strand a child under the preserved path'
		When call run_revert_preflight coherent zelta revert -qqn --exclude='*/child*' apool/source@old
		The status should be failure
		The error should include 'unselected dataset'
		The path "$SHELLSPEC_TMPBASE/revert-mutations-coherent" should not be exist
	End

	It 'executes a coherent recursive revert after validation'
		When call run_revert_preflight coherent-real zelta revert -qq apool/source@old
		The status should be success
		The path "$SHELLSPEC_TMPBASE/revert-mutations-coherent-real" should be exist
	End

	Parameters
		--force force-long
		-f force-short
	End
	It "permits the old incomplete clone plan with $1"
		When call run_revert_preflight "$2" zelta revert -n "$1" apool/source@old
		The status should be success
		The output should include "'apool/source_old/child with space@old'"
		The error should include 'forcing best-effort revert'
		The path "$SHELLSPEC_TMPBASE/revert-mutations-$2" should not be exist
	End

	It "reports an incomplete revert with $1"
		When call run_revert_preflight "failure-$2" zelta revert -qq "$1" apool/source@old
		The status should be failure
		The error should eq "error: error creating clone 'apool/source/child with space'
error: revert incomplete: 1 cloned, 1 failed, 0 skipped; original tree preserved at 'apool/source_old'"
		The path "$SHELLSPEC_TMPBASE/revert-mutations-failure-$2" should be exist
	End

	It "continues siblings but skips dependent descendants with $1"
		When call run_best_effort_revert "best-effort-$2" "$1"
		The status should be failure
		The error should include "skipping clone 'apool/source/child with space/dependent': parent 'apool/source/child with space' was not restored"
		The error should include "revert incomplete: 3 cloned, 1 failed, 1 skipped; original tree preserved at 'apool/source_old'"
		The output should eq 'rename -fp apool/source apool/source_old
clone -p -o readonly=off apool/source_old@old apool/source
clone -p -o readonly=off apool/source_old/child with space@old apool/source/child with space
clone -p -o readonly=off apool/source_old/child with space2@old apool/source/child with space2
clone -p -o readonly=off apool/source_old/zhealthy@old apool/source/zhealthy'
	End
End

# Independent sibling fixture: no changes to the standard suite's source tree.
# The suite's existing pool cleanup also removes this fixture and its preserve.
exercise_best_effort_zfs() {
	_probe_ds="${SANDBOX_ZELTA_SRC_DS}_force_probe_${SANDBOX_ZELTA_TMP_SUFFIX}"
	_probe_ep="${SANDBOX_ZELTA_SRC_EP}_force_probe_${SANDBOX_ZELTA_TMP_SUFFIX}"
	src_exec zfs create -u "$_probe_ds" || return 1
	src_exec zfs create -u "$_probe_ds/zhealthy" || return 1
	src_exec zfs snapshot -r "$_probe_ds@baseline" || return 1
	src_exec zfs create -u "$_probe_ds/tmp" || return 1
	src_exec zfs snapshot -r "$_probe_ds@later" || return 1
	if zelta revert -qq -f "$_probe_ep@baseline"; then
		return 1
	fi
	_origin=$(src_exec zfs get -Ho value origin "$_probe_ds/zhealthy") || return 1
	[ "$_origin" = "${_probe_ds}_baseline/zhealthy@baseline" ] || return 1
	src_exec zfs list -Ho name "${_probe_ds}_baseline/tmp" >/dev/null || return 1
	if src_exec zfs list "$_probe_ds/tmp" >/dev/null 2>&1; then
		return 1
	fi
	_snapshots=$(src_exec zfs list -Hro name -t snapshot "$_probe_ds") || return 1
	[ -z "$_snapshots" ] || return 1
	zelta clone -qq "${_probe_ep}_baseline/tmp@later" "$_probe_ep/tmp" || return 1
	_origin=$(src_exec zfs get -Ho value origin "$_probe_ds/tmp") || return 1
	[ "$_origin" = "${_probe_ds}_baseline/tmp@later" ]
}

Describe 'Best-effort revert on ZFS' standard:51
	Skip if 'SANDBOX_ZELTA_SRC_POOL undefined' test -z "$SANDBOX_ZELTA_SRC_POOL"
	Skip if 'SANDBOX_ZELTA_SRC_DS undefined' test -z "$SANDBOX_ZELTA_SRC_DS"

	It 'restores the later sibling despite a newer tmp child, then clones tmp back explicitly'
		When call exercise_best_effort_zfs
		The status should be success
		The error should include "error creating clone '${SANDBOX_ZELTA_SRC_DS}_force_probe_${SANDBOX_ZELTA_TMP_SUFFIX}/tmp'"
		The error should include 'revert incomplete: 2 cloned, 1 failed, 0 skipped'
	End
End

Describe 'Best-effort revert dependency handling' standard:51
	It 'does not create synthetic parents when the root clone fails'
		When call run_best_effort_revert root-failure -f
		The status should be failure
		The error should include 'revert incomplete: 0 cloned, 1 failed, 4 skipped'
		The output should eq 'rename -fp apool/source apool/source_old
clone -p -o readonly=off apool/source_old@old apool/source'
	End

	It 'counts children without snapshots as an incomplete result'
		When call run_best_effort_revert no-snapshot -f
		The status should be failure
		The error should include "skipping clone 'apool/source/child with space': no source snapshot"
		The error should include 'revert incomplete: 1 cloned, 0 failed, 1 skipped'
		The output should eq 'rename -fp apool/source apool/source_old
clone -p -o readonly=off apool/source_old@old apool/source'
	End

	It 'snapshots when a forced revert is complete'
		When call run_best_effort_revert coherent-force -f
		The status should be success
		The output should include 'snapshot -r apool/source@zelta_'
	End

	It 'does not recommend rotate after an incomplete forced revert'
		When call run_revert_preflight guidance-failure zelta revert -f apool/source@old
		The status should be failure
		The output should include 'cloned 1/2 datasets'
		The output should not include 'to retain replica history'
		The error should include 'revert incomplete:'
	End

	It 'reports an incomplete dry run without claiming anything was preserved'
		When call run_revert_preflight no-snapshot-dryrun zelta revert -n -f apool/source@old
		The status should be failure
		The output should not include 'would recommend:'
		The error should include "revert would be incomplete: 1 planned, 1 skipped; original tree would be preserved at 'apool/source_old'"
		The path "$SHELLSPEC_TMPBASE/revert-mutations-no-snapshot-dryrun" should not be exist
	End
End
