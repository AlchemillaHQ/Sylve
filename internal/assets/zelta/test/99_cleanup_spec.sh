Describe 'Cleanup'
    Describe 'Dataset cleanup'
        It 'destroy source dataset'
            Skip if 'dataset not created in this run' tmpfile_check divergent_tree_created
            When call clean_src_ds
            The status should be success
        End
        It 'destroy target dataset'
            Skip if 'dataset not created in this run' tmpfile_check divergent_tree_created
            When call clean_tgt_ds
            The status should be success
        End
    End
    Describe 'Pool cleanup'
        It 'destroy source'
            Skip if 'pool not created in this run' tmpfile_check src_pool_created
            When call nuke_src_pool
            The status should be success
        End
        It 'destroy target'
            Skip if 'pool not created in this run' tmpfile_check tgt_pool_created
            When call nuke_tgt_pool
            The status should be success
        End
    End
    Describe 'Installation cleanup'
        It 'uninstall script'
            When run sh uninstall.sh env
            The status should be success
            The output should include 'removing'
        End
        It 'remove temporary installation'
            When call cleanup_temp_install
            The status should be success
            The output should include '2'
        End
    End
End
