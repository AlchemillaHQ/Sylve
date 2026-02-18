# This is a helper for debugging and testing your generated tests
# This can be helpful if you use the generate_new_tests.sh approach
# and your test confirmation fails.
#
# Why? Because you can fine-tune the tree setup and run your test commands
# by hand to determine the cause of problems. Your test yml definition may
# need additional setup or a modified command. So the ability to iteratively
# test out the new spec can help you resolve problems.
#
# SPECS    - the shellspec examples you run to setup the tree before running your test
# NEW_SPEC - the generated test you are debugging or verifying

REPO_ROOT=$(git rev-parse --show-toplevel)

# standard locations under test
RUNNERS_DIR="$REPO_ROOT/test/runners"
TEST_GEN_DIR="$REPO_ROOT/test/runners/test_generation"

# tree setup utility
. "$TEST_GEN_DIR/lib/orchestration/setup_tree.sh"

# prepare the zfs tree with the state represented by running the following examples/tests
SPECS="test/01*_spec.sh|test/01*_spec.sh|test/02*_spec.sh|test/040_*_spec.sh"

# use the directory where your generated test is created
# by default we're using a temp directory off of test/runners/test_generation
NEW_SPEC="$TEST_GEN_DIR/tmp/050_zelta_revert_spec.sh"
echo "confirming new spec: {$NEW_SPEC}"

if setup_tree "$SPECS"; then
    printf "\n ✅ initial tree setup succeeded for specs: %s\n" "$SPECS"
else
    printf "\n ❌ Goodbye, initial tree setup failed for specs: %s\n" "$SPECS"
    exit 1
fi

#
. "$RUNNERS_DIR/env/setup_debug_env.sh"

# show a detailed trace of the commands you are executing in your new test
TRACE_OPTIONS="--xtrace --shell /opt/homebrew/bin/bash"
# if you don't want/need a detailed trace unset the options var
#unset TRACE_OPTIONS

# run the new test, show the outcome
if shellspec $TRACE_OPTIONS "$NEW_SPEC"; then
    printf "\n ✅ confirmation test succeeded for new spec: %s\n" "$NEW_SPEC"
else
    printf "\n ❌ confirmation test failed for new spec: %s\n" "$NEW_SPEC"
    exit 1
fi
