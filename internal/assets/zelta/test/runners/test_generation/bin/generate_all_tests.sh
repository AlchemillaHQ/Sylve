#!/usr/bin/env bash
# Get the directory where this script is located
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

set -e

"$SCRIPT_DIR/generate_40_divergent_test.sh"
"$SCRIPT_DIR/generate_50_revert_test.sh"
"$SCRIPT_DIR/generate_60_clone_test.sh"
"$SCRIPT_DIR/generate_70_prune_test.sh"
"$SCRIPT_DIR/generate_80_policy_test.sh"
