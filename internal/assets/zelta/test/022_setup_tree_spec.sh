# Check remotes and create pools and datasets

Describe 'Divergent tree tests'
    # TODO: Check with DB what env vars he wants to use for Skip if
    Skip if 'SANDBOX_ZELTA_SRC_DS and SANDBOX_ZELTA_TGT_DS are undefined' test -z "$SANDBOX_ZELTA_SRC_DS" -a -z "$SANDBOX_ZELTA_TGT_DS"

    Describe 'setup'
        It 'creates initial tree on source'
            When call make_initial_tree
            The status should be success
        End

        It 'can zelta backup initial tree'
            When call zelta backup --snap-name @start "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
            The status should be success
            The output should include 'syncing 9 datasets'
            The error should not include 'error:'
        End

        It 'can create tree divergence'
            When call make_tree_divergence
            The status should be success
            The error should not include 'error:'
        End
    End

    Describe 'zelta match'
        It 'shows expected divergence types'
            When run zelta match "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
            The status should be success
            The output should include 'up-to-date'
            The output should include 'syncable (full)'
            The output should include 'syncable (incremental)'
            The output should include 'blocked sync: target diverged'
            The output should include 'blocked sync: no target snapshots'
            The output should include '11 total datasets compared'
        End
    End
End
