# CAUTION: Use only for test generation scenarios
# - Test generation clean up only requires TESTGEN_ZELTA_DESTRUCTIVE and SANDBOX_ZELTA_TMP_SUFFIX to be defined
# - This check is less restrictive from the normal test cleanup for existing tests, which performs tmpfile_check
# - Test environments are volatile, don't use these targets outside of test generation and bespoke test scenarios
#
Describe 'DestroyTestEnv' testgen-destroy
    Skip if 'TESTGEN_ZELTA_DESTRUCTIVE is undefined, unconditional test env destroy not allowed' test -z "$TESTGEN_ZELTA_DESTRUCTIVE"
    Skip if 'SANDBOX_ZELTA_TMP_SUFFIX is undefined, must be defined for test env' test -z "$SANDBOX_ZELTA_TMP_SUFFIX"

    Describe 'Dataset destroy'
        # NOTE: less restrictive than normal cleanup, not tmpfile_check
        It 'destroy source dataset'
            Skip if 'SANDBOX_ZELTA_SRC_DS is undefined' test -z "$SANDBOX_ZELTA_SRC_DS"
            When call clean_src_ds
            The status should be success
        End
        It 'destroy target dataset'
            Skip if 'SANDBOX_ZELTA_TGT_DS is undefined' test -z "$SANDBOX_ZELTA_TGT_DS"
            When call clean_tgt_ds
            The status should be success
        End
    End

    Describe 'Pool destroy'
        # NOTE: less restrictive than normal cleanup, not tmpfile_check
        It 'destroy source'
            Skip if 'SANDBOX_ZELTA_SRC_POOL undefined' [ -z "$SANDBOX_ZELTA_SRC_POOL" ]
            When call nuke_src_pool
            The status should be success
        End
        It 'destroy target'
            Skip if 'SANDBOX_ZELTA_TGT_POOL undefined' [ -z "$SANDBOX_ZELTA_TGT_POOL" ]
            When call nuke_tgt_pool
            The status should be success
        End
    End

    Describe 'Installation cleanup'
        Skip if 'SANDBOX_ZELTA_TMP_SUFFIX is undefined' test -z "$SANDBOX_ZELTA_TMP_SUFFIX"
        It 'uninstall script'
            When run sh uninstall.sh env
            The output should satisfy noop
            The error  should satisfy noop
            The status should satisfy noop
        End
        It 'remove temporary installation'
            When call cleanup_temp_install
            The output should satisfy noop
            The error  should satisfy noop
            The status should satisfy noop
        End
    End
End
