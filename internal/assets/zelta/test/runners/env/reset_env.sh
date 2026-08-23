# source this file to ensure that the next run of shellspec will pick up
# the standard test environment for zelta that gets created by test/test_helper.sh

echo "unsetting SANDBOX_ZELTA_TMP_DIR to forces test_helper.sh to re-evaluate ZELTA setup"
unset SANDBOX_ZELTA_TMP_DIR # forces test_helper.sh to re-evaluate ZELTA setup

. ./test/runners/env/test_env.sh
