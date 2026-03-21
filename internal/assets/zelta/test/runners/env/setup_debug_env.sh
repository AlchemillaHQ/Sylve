# SPDX-License-Identifier: BSD-2-Clause
#
# Copyright (c) 2025 The FreeBSD Foundation.
#
# This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
# of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
# under sponsorship from the FreeBSD Foundation.

# source this file to setup the zelta debug environment
# returns 0 on failure

printf "\n*\n* Running in DEBUG MODE, sourcing setup files\n*\n"
# use debug env, the last version of zelta installed"

if . test/runners/env/set_reuse_tmp_env.sh; then
   . test/runners/env/test_env.sh     # set dataset, pools and remote env vars
   . test/test_helper.sh          # make all the helper functions available
else
    return 1
fi
