# Check `zelta rebase` option processing and dry-run planning

Describe 'zelta rebase'
	It 'requires two operands'
		When run command zelta rebase old-prod
		The status should be failure
		The error should include 'requires UPSTREAM and TARGET'
	End
	It 'shows the rebase backup primitive in dry-run mode'
		When run command zelta rebase -n upstream target
		The status should be success
		The output should include "zelta ipc-run backup --target-origin"
	End
	It 'plans preserve-file copy commands in dry-run mode'
		When run command zelta rebase -n --rebase-file examples/bsdcan/rebase/web01.zeltarebase upstream target
		The status should be success
		The output should include "cp -p"
	End
End
