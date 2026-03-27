#!/usr/bin/env bash
# Get the directory where this script is located
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

# SPECS    - the shellspec examples you run to setup the tree before running your test
# prepare the zfs tree with the state represented by running the following examples/tests
# TODO: change the SPECS variable to include all tests you need to run before the new test
SPECS="test/01*_spec.sh|test/02*_spec.sh|test/03_*_spec.sh|test/040_*_spec.sh|test/050_*_spec.sh|test/060_*_spec.sh"

if ! . "$SCRIPT_DIR/setup_debug_state.bash" "$SPECS"; then
    printf "\n ❌ ERROR: debug state setup failed\n"
    exit 1
fi

# use the directory where your generated test is created
# by default we're using a temp directory off of test/runners/test_generation
# TODO: change the NEWS_SPEC variable to be spec of the new test you debugging
NEW_SPEC="$TEST_GEN_DIR/tmp/070_zelta_prune_spec.sh"
echo "confirming new spec: {$NEW_SPEC}"


# show a detailed trace of the commands you are executing in your new test
# macOS BASH_SH=/opt/homebrew/bin/bash
# Arch  BASH_SH=/usr/bin/bash
BASH_SH=/usr/bin/bash
TRACE_OPTIONS="--xtrace --shell $BASH_SH"
# if you don't want/need a detailed trace unset the options var
#unset TRACE_OPTIONS

# run the new test, show the outcome
if shellspec $TRACE_OPTIONS "$NEW_SPEC"; then
    printf "\n ✅ confirmation test succeeded for new spec: %s\n" "$NEW_SPEC"
else
    printf "\n ❌ confirmation test failed for new spec: %s\n" "$NEW_SPEC"
    exit 1
fi
