# source this file to ensure that the next run of shellspec will pick up
# the standard test environment for zelta that gets created by test/test_helper.sh

TEST_DIR="test"
echo "unsetting SANDBOX_ZELTA_TMP_SUFFIX to forces test_helper.sh to re-evaluate ZELTA setup"
unset SANDBOX_ZELTA_TMP_SUFFIX # forces test_helper.sh to re-evaluate ZELTA setup

. "$TEST_DIR/runners/env/test_env.sh"
