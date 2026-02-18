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
