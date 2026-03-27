#!/usr/bin/env bash

# called by shellspec test for policy, this script
# will generate a basic zelta policy file for the
# current SANDBOX environment variables

# Get the directory where this script is located
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TEST_GEN_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG_DIR="$TEST_GEN_DIR/config"
ZELTA_TEST_POLICY_CONFIG_FILE="$CONFIG_DIR/zelta_test_policy.conf"


if [ -n "$SANDBOX_ZELTA_SRC_REMOTE" ]; then
   src_host="$SANDBOX_ZELTA_SRC_REMOTE"
else
   src_host="localhost"
fi

tgt_host=""
if  [ -n "${SANDBOX_ZELTA_TGT_REMOTE}" ]; then
   tgt_host="${SANDBOX_ZELTA_TGT_REMOTE}:"
fi

# remove any existing policy file
rm -f "$ZELTA_TEST_POLICY_CONFIG_FILE"

CUR_TIME_STAMP=$(date -u +%Y-%m-%d_%H.%M.%S)
BACKUP_NAME="zelta_policy_backup_${CUR_TIME_STAMP}"

# generate new policy file
cat <<EOF > $ZELTA_TEST_POLICY_CONFIG_FILE
# shellspec auto generated test zelta policy file at: ($CUR_TIME_STAMP)
# NOTE: any modification will be lost
BACKUP_SITE:
  ${src_host}:
    datasets:
    - ${SANDBOX_ZELTA_SRC_DS}: ${SANDBOX_ZELTA_TGT_EP}
EOF
