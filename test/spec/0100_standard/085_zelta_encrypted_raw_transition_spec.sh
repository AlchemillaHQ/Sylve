# Test encrypted raw-to-nonraw replication transition

enc_raw_init() {
	EncRawSuffix="/enc-raw-transition"
	EncRawSrcDS="${SANDBOX_ZELTA_SRC_DS}${EncRawSuffix}"
	EncRawTgtDS="${SANDBOX_ZELTA_TGT_DS}${EncRawSuffix}"
	EncRawKey="/tmp/sandbox-zelta-test-key_${SANDBOX_ZELTA_PROCNUM}"

	if [ -n "$SANDBOX_ZELTA_SRC_REMOTE" ]; then
		EncRawSrcEP="${SANDBOX_ZELTA_SRC_REMOTE}:${EncRawSrcDS}"
	else
		EncRawSrcEP="$EncRawSrcDS"
	fi

	if [ -n "$SANDBOX_ZELTA_TGT_REMOTE" ]; then
		EncRawTgtEP="${SANDBOX_ZELTA_TGT_REMOTE}:${EncRawTgtDS}"
	else
		EncRawTgtEP="$EncRawTgtDS"
	fi
}

enc_create_key_for_src_and_tgt() {
    dd if=/dev/urandom bs=32 count=1 of="$EncRawKey" 2>/dev/null
    chmod 600 "$EncRawKey"
    scp -p "$EncRawKey" "$SANDBOX_ZELTA_SRC_REMOTE:$EncRawKey"
    scp -p "$EncRawKey" "$SANDBOX_ZELTA_TGT_REMOTE:$EncRawKey"	
}


enc_raw_cleanup() {
	enc_raw_init
	tgt_exec zfs destroy -r "$EncRawTgtDS" >/dev/null 2>&1 || :
	src_exec zfs destroy -r "$EncRawSrcDS" >/dev/null 2>&1 || :
	tgt_exec rm -f "$EncRawKey" >/dev/null 2>&1 || :
	src_exec rm -f "$EncRawKey" >/dev/null 2>&1 || :
	return 0
}

enc_raw_setup() {
	enc_raw_init
	enc_raw_cleanup || return 1
	enc_create_key_for_src_and_tgt
	src_exec zfs create -u -o encryption=on -o keyformat=raw -o "keylocation=file://$EncRawKey" "$EncRawSrcDS" || return 1
	return 0
}

enc_raw_initial_backup() {
	enc_raw_init
	zelta backup --log-level 4 "$EncRawSrcEP" "$EncRawTgtEP" 2>&1
}

enc_raw_snapshot_backup_raw() {
	enc_raw_init
	sleep 1
	zelta backup --log-level 4 --snapshot "$EncRawSrcEP" "$EncRawTgtEP" 2>&1
}

enc_raw_load_target_key() {
        enc_raw_init
	tgt_exec zfs load-key -L "file://$EncRawKey" "$EncRawTgtDS"
}

enc_raw_snapshot_backup_override_nonraw() {
	enc_raw_init
	sleep 1
	zelta backup --log-level 4 -Lc --snapshot "$EncRawSrcEP" "$EncRawTgtEP" 2>&1
}

enc_raw_snapshot_backup_fallback_nonraw() {
	enc_raw_init
	sleep 1
	zelta backup --log-level 4 --snapshot "$EncRawSrcEP" "$EncRawTgtEP" 2>&1
}

Describe 'Encrypted raw-send transition' standard:85
	Skip if 'SANDBOX_ZELTA_SRC_DS undefined' test -z "$SANDBOX_ZELTA_SRC_DS"
	Skip if 'SANDBOX_ZELTA_TGT_DS undefined' test -z "$SANDBOX_ZELTA_TGT_DS"
	Skip if 'requires separate source and target remotes' test -z "$SANDBOX_ZELTA_SRC_REMOTE" -o -z "$SANDBOX_ZELTA_TGT_REMOTE" -o "$SANDBOX_ZELTA_SRC_REMOTE" = "$SANDBOX_ZELTA_TGT_REMOTE"

	It 'creates an encrypted dataset with a key file'
		When call enc_raw_setup
		The status should be success
	End

	It 'replicates the first backup as raw'
		When call enc_raw_initial_backup
		The status should be success
		The output should include 'syncing 1 datasets'
		The output should include 'zfs send -P --raw'
		The output should not include 'raw incremental unavailable'
	End

	It 'replicates the second backup with --snapshot as raw'
		When call enc_raw_snapshot_backup_raw
		The status should be success
		The output should include 'snapshotting: @zelta_'
		The output should include 'zfs send -P --raw -I'
		The output should not include 'raw incremental unavailable'
	End

	It 'loads the target key'
		When call enc_raw_load_target_key
		The status should be success
	End

	It 'sends non-raw for --snapshot with -Lc after the target key is loaded'
		When call enc_raw_snapshot_backup_override_nonraw
		The status should be success
		The output should include 'zfs send -P -L -c -I'
		The output should not include 'zfs send -P --raw -I'
		The output should not include 'raw incremental unavailable'
	End

	It 'complains and sends non-raw for later default --snapshot backups'
		When call enc_raw_snapshot_backup_fallback_nonraw
		The status should be success
		The output should include 'raw incremental unavailable at @zelta_'
		The output should include "falling back to decrypted send: ${SANDBOX_ZELTA_SRC_DS}/enc-raw-transition"
		The output should include 'zfs send -P -L -c -I'
		The output should not include 'zfs send -P --raw -I'
	End

	It 'cleans up the encrypted transition dataset'
		When call enc_raw_cleanup
		The status should be success
	End
End
