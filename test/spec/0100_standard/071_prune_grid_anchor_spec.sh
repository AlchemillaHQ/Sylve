# Check prune-grid anchoring with synthetic zfs list output.

setup_prune_grid_fake_zfs() {
	_fakebin="$SANDBOX_ZELTA_TMP_DIR/fakebin"
	mkdir -p "$_fakebin"
	cat > "$_fakebin/zfs" <<'EOF'
#!/bin/sh

_dataset=
for _arg do
	_dataset="$_arg"
done

case "$_dataset" in
apool/treetop)
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' 'apool/treetop' '1' '0' '1209600' '0' '0' '-'
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' 'apool/treetop@zelta_match' '100' '0' '1209600' '0' '0' '-'
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' 'apool/treetop@zelta_s3' '101' '0' '950400' '0' '0' '-'
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' 'apool/treetop@zelta_s6' '102' '0' '691200' '0' '0' '-'
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' 'apool/treetop@zelta_s8' '103' '0' '518400' '0' '0' '-'
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' 'apool/treetop@zelta_s12' '104' '0' '172800' '0' '0' '-'
	;;
bpool/backups)
	printf '%s\t%s\t%s\t%s\t%s\t%s\n' 'bpool/backups' '10' '0' '1209600' '0' '0'
	printf '%s\t%s\t%s\t%s\t%s\t%s\n' 'bpool/backups@zelta_match' '100' '0' '1209600' '0' '0'
	;;
*)
	exit 1
	;;
esac
EOF
	chmod +x "$_fakebin/zfs"
	FAKE_ZFS_PATH="$_fakebin:$PATH"
	export FAKE_ZFS_PATH
}

skip_without_install() {
	check_install
	[ $? -ne 0 ]
}

Describe 'Prune grid anchor' standard:71
	Skip if 'temporary install missing' skip_without_install

	It 'anchors grid buckets to the latest match instead of execution time'
		setup_prune_grid_fake_zfs
		When run command env PATH="$FAKE_ZFS_PATH" ZELTA_SYSTIME='printf "%s\n" 1468800' zelta prune --no-ranges --prune-num=0 --prune-time=0 --prune-grid=1200x6day apool/treetop bpool/backups
		The output should include 'apool/treetop@zelta_s8'
		The output should not include 'apool/treetop@zelta_s6'
		The status should be success
	End
End
