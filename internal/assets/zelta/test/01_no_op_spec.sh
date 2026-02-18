# Check Zelta usage, help, version, and `zelta match` option processing

Describe 'Zelta no-op command checks'
    Describe 'zelta command'
        It 'is executable'
            When run command which zelta
            The status should be success
            The output should include 'zelta'
        End
        It 'shows usage with no arguments'
            When run command zelta
            The status should be failure
            The error should include 'usage'
        End
        It 'shows version'
            When run command zelta version
            The status should be success
            The output should include 'Zelta'
        End
        It 'shows man page'
            When run command zelta help
            The status should be success
            The output should include 'zelta(8)'
        End
    End
    Describe 'zelta match'
    	It 'shows zfs list commands for one operand'
    		When run command zelta match --dryrun zelta-test-pool/test-source
    		The status should be success
    		The output should include '+ zfs list'
    	End
    	It 'shows zfs list commands for two operands'
    		When run command zelta match --dryrun zelta-test-pool/test-source zelta-test-pool/test-target
    		The status should be success
    		The output should include '+ zfs list'
    	End
    	It 'respects single-dash parameters'
    		When run command zelta match  -Hpvqn -X '*/swap' -d42 -o ds_suffix zelta-test-pool/test
    		The status should be success
    		The output should include '42'
    	End
    	It 'respects all parameters'
    		When run command zelta match  -Hpvqn -X '*/swap' -d42 -o ds_suffix --verbose --quiet --log-level 2 --log-mode=text --text --dryrun --depth 42 --exclude="@one,/two" zelta-test-pool/test zelta-test-pool/test-target
    		The status should be success
    		The output should include '42'
    	End
    End
End
