# SPDX-License-Identifier: BSD-2-Clause
#
# Copyright (c) 2025 The FreeBSD Foundation.
#
# This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
# of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
# under sponsorship from the FreeBSD Foundation.

# Modify this file to configure your test pools, datasets and endpoints

# pools
export SANDBOX_ZELTA_SRC_POOL=apool
export SANDBOX_ZELTA_TGT_POOL=bpool

# datasets
export SANDBOX_ZELTA_SRC_DS=apool/treetop
export SANDBOX_ZELTA_TGT_DS=bpool/backups

# remotes setup
#   * leave these undefined if you're running locally
#   * the endpoints are defined automatically and are REMOTE + DS
export SANDBOX_ZELTA_SRC_REMOTE=dever@zfsdev # Ubuntu source
export SANDBOX_ZELTA_TGT_REMOTE=dever@zfsdev # Ubuntu remote
