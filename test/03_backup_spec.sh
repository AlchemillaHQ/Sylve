Describe 'Backup tests'
    It 'no-op all options'
        Skip if 'SANDBOX_ZELTA_TGT_DS undefined' test -z "$SANDBOX_ZELTA_TGT_DS"
        When call backup_no_op_check
        The status should be success
        # In json mode, all unsuppressed notices will be stderr
        The error should include 'would snapshot'
        The error should include 'zfs snapshot'
        The error should include 'diverged'
        The error should not include 'snapshotting'
        The error should not include 'error:'
        # Check json
        The output should include 'output_version'
    End
    It 'valid json'
        Skip if 'SANDBOX_ZELTA_TGT_DS undefined' test -z "$SANDBOX_ZELTA_TGT_DS"
        Skip if 'jq required' test -z "$(command -v jq)"
        When call backup_check_json
        The status should be success
        The output should include 'zelta backup'
    End
End
