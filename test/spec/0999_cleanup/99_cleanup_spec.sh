Describe 'Cleanup' cleanup
    Describe 'Dataset cleanup' cleanup:datasets
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
    Describe 'Pool cleanup' cleanup:pools
#        It 'tests if pool created'
#            Skip if 'pool not created in this run' tmpfile_check src_pool_created
#            When call tmpfile_check divergent_tree_created
#            The status should be failure
#        End
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
    Describe 'Installation cleanup' cleanup:install
        It 'uninstall script'
            When run sh uninstall.sh env
            The status should be success
            The output should include 'removing'
        End
        It 'remove temporary installation'
            When call cleanup_temp_install
            The status should be success
            #The output should include '5'
            The output should be present
        End
    End
End
