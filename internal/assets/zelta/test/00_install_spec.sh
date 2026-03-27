# Install Zelta

Describe 'Zelta installation'
    It 'runs installer without errors'
        When run ./install.sh
        The status should be success
        The output should include 'installing'
    End
    It 'check installed files'
        When call check_install
        The status should be success
    End
End
