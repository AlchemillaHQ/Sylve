#!/usr/bin/env bash

# *** only source this script in bash, do not execute it

# This is a helper for setting up a test environment with the datasets
# as they would exist after running all the specs defined by SPECS
#
# This can be helpful if you use the generate_new_tests.sh approach
# and your test confirmation fails.
#
# Why? Because you can fine-tune the tree setup and run your test commands
# by hand to determine the cause of problems. Your test yml definition may
# need additional setup or a modified command. So the ability to iteratively
# test out the new spec can help you resolve problems.

REPO_ROOT=$(git rev-parse --show-toplevel)

# standard locations under test
RUNNERS_DIR="$REPO_ROOT/test/runners"
TEST_GEN_DIR="$REPO_ROOT/test/runners/test_generation"

# Requires Bash — will produce syntax errors in fish, csh, etc.
# Usage: source setup.bash

# Guard: reject non-Bash POSIX shells (sh, dash, zsh)
if [ -z "$BASH_VERSION" ] || case "$SHELLOPTS" in *posix*) true;; *) false;; esac; then
    echo "Error: This script requires Bash (not sh)." >&2
    return 1 2>/dev/null || exit 1
fi

# Guard: reject direct execution
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    echo "Error: This script must be sourced, not executed." >&2
    echo "Usage: source ${BASH_SOURCE[0]}" >&2
    return 1
fi

# Check if arguments were provided
if [ $# -lt 1 ] || [ $# -gt 2 ]; then
    echo "Invalid number of arguments!"
    echo "Usage: $0 'pattern specs list' 'selector specs list"
    return 1
fi

# tree setup utility
. "$TEST_GEN_DIR/lib/orchestration/setup_tree.sh"

if setup_tree "$@"; then
    printf "\n ✅ initial tree setup succeeded for specs: %s\n" "$@"
else
    printf "\n ❌ Goodbye, initial tree setup failed for specs: %s\n" "$@"
    return 1
fi

# setup the debugging environment
. "$RUNNERS_DIR/env/setup_debug_env.sh"

# now you can execute commands as if they were in a test
# that runs after all the SPECS hae completed
