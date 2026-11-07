# Name filtering must not hide guard matches or join ranges across other names.

run_prune_name() {
	_fakebin="$SHELLSPEC_TMPBASE/prune-name-fakebin"
	mkdir -p "$_fakebin"
	cat > "$_fakebin/zfs" <<'EOF'
#!/bin/sh
_command=$1
for _arg do
	_dataset=$_arg
done
if [ "$_command" = destroy ]; then
	printf '%s\n' "$_dataset"
	exit 0
fi
[ "$_command" = list ] || exit 1
row() {
	printf '%s\t%s\t0\t%s\t0\t0\t%s\n' "$1" "$2" "$3" "${4:--}"
}
case "$_dataset" in
apool/treetop)
	row apool/treetop 1 1209600
	row apool/treetop@autosnap_new 120 1209600
	row apool/treetop@manual_guard 110 1123200
	row apool/treetop@autosnap_clone 109 1036800 apool/clone
	row apool/treetop@autosnap_old2 108 950400
	row apool/treetop@manual_between 107 864000
	row apool/treetop@autosnap_old1 106 777600
	row apool/treetop@autosnap_old0 105 691200
	row apool/treetop@auto-old 104 604800
	row apool/treetop@zfsnap_old 103 518400
	row apool/treetop@zelta.1 102 432000
	row apool/treetop@zeltaX1 101 345600
	;;
bpool/backups)
	row bpool/backups 10 1209600
	row bpool/backups@manual_guard 110 1123200
	;;
*) exit 1 ;;
esac
EOF
	chmod +x "$_fakebin/zfs"
	_repo="${REPO_ROOT:-$SHELLSPEC_PROJECT_ROOT}"
	env PATH="$_fakebin:$_repo/bin:$PATH" ZELTA_SHARE="$_repo/share/zelta" \
		ZELTA_ENV=/dev/null "$@"
}

Describe 'Prune name selector' standard:72
	It 'limits Sanoid candidates to autosnap names'
		When call run_prune_name zelta prune --no-ranges --prune-num=0 --prune-name=sanoid apool/treetop
		The output should eq 'apool/treetop@autosnap_old0
apool/treetop@autosnap_old1
apool/treetop@autosnap_old2
apool/treetop@autosnap_new'
		The status should be success
	End

	It 'selects TrueNAS snapshots'
		When call run_prune_name zelta prune --prune-num=0 --prune-name=truenas apool/treetop
		The output should eq 'apool/treetop@auto-old'
		The status should be success
	End

	It 'selects zfsnap snapshots'
		When call run_prune_name zelta prune --prune-num=0 --prune-name=zfsnap apool/treetop
		The output should eq 'apool/treetop@zfsnap_old'
		The status should be success
	End

	It 'supports custom snapshot globs'
		When call run_prune_name zelta prune --prune-num=0 '--prune-name=zelta.?' apool/treetop
		The output should eq 'apool/treetop@zelta.1'
		The status should be success
	End

	It 'passes destroy ranges without including intervening manual snapshots'
		When call run_prune_name zprune -qqf --no-prune-guard --prune-num=0 --prune-name=sanoid apool/treetop
		The output should eq 'apool/treetop@autosnap_new,autosnap_old2,autosnap_old0%autosnap_old1'
		The status should be success
	End

	It 'preserves the nonmatching guard boundary and clone origins'
		When call run_prune_name zelta prune --no-ranges --prune-num=0 --prune-name=sanoid --match-endpoint=bpool/backups apool/treetop
		The output should eq 'apool/treetop@autosnap_old0
apool/treetop@autosnap_old1
apool/treetop@autosnap_old2'
		The status should be success
	End

	It 'still applies include, exclude, and count retention'
		When call run_prune_name zelta prune --prune-num=1 --prune-name=sanoid '--include=@autosnap_old*' '--exclude=@autosnap_old2' apool/treetop
		The output should eq 'apool/treetop@autosnap_old0'
		The status should be success
	End
End
