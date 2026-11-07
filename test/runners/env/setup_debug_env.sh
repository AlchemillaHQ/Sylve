# source this file to setup the zelta debug environment
# returns 0 on failure

TEST_DIR="test"

printf "\n*\n* Running in DEBUG MODE, sourcing setup files\n*\n"
# use debug env, the last version of zelta installed"

if . "$TEST_DIR/runners/env/set_reuse_tmp_env.sh"; then
   . "$TEST_DIR/runners/env/test_env.sh"     # set dataset, pools and remote env vars
   . "$TEST_DIR/spec/helpers/spec_helper.sh"              # make all the helper functions available
   env | grep -i sandbox
else
    return 1
fi
