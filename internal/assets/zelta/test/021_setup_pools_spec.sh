# SPDX-License-Identifier: BSD-2-Clause
#
# Copyright (c) 2025 The FreeBSD Foundation.
#
# This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
# of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
# under sponsorship from the FreeBSD Foundation.

Describe 'Remote check'
    It 'source accessible'
        Skip if 'SANDBOX_ZELTA_SRC_REMOTE undefined' [ -z "$SANDBOX_ZELTA_SRC_REMOTE" ]
        When run ssh -n "$SANDBOX_ZELTA_SRC_REMOTE" true
        The status should be success
    End
    It 'target accessible'
        Skip if 'SANDBOX_ZELTA_TGT_REMOTE undefined' [ -z "$SANDBOX_ZELTA_TGT_REMOTE" ]
        When run ssh -n "$SANDBOX_ZELTA_TGT_REMOTE" true
        The status should be success
    End
End

Describe 'Pool setup'
    It 'create source'
        Skip if 'SANDBOX_ZELTA_SRC_POOL undefined' [ -z "$SANDBOX_ZELTA_SRC_POOL" ]
        When call make_src_pool
        The status should be success
    End
    It 'create target'
        Skip if 'SANDBOX_ZELTA_TGT_POOL undefined' [ -z "$SANDBOX_ZELTA_TGT_POOL" ]
        When call make_tgt_pool
        The status should be success
    End
End