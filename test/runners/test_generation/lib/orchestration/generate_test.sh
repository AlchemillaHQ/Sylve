#!/bin/sh

# Check for required arguments
if [ $# -lt 2 ]; then
    printf "Usage: %s <test_config> <setup_specs>\n" "$0" >&2
    printf "\t-> *  setup zfs tree with state represented by <setup_specs>\n"
    printf "\t-> *  use the <test_config> to generate a test\n"
    printf "\t-> *  setup zfs tree again\n"
    printf "\t-> *  test the generated test from <test_config>\n"
    printf "\t-> *  if test passes, move it to production\n"
    printf "example:\n"
    printf "%s \ \n" "$0"
    printf " test_defs/050_zelta_revert_test.yml \ \n"
    printf " test/00*_spec.sh|test/01*_spec.sh|test/02*_spec.sh|test/040_zelta_tests_spec.sh\n"
    exit 1
fi

TEST_CONFIG=$1
SETUP_TREE_SPECS=$2
REPO_ROOT=$(git rev-parse --show-toplevel)

PROD_TEST_DIR="$REPO_ROOT/test"
TEST_GEN_DIR="$REPO_ROOT/test/runners/test_generation"
GENERATED_TEST_NAME=""
GENERATED_TEST_PATH=""
PROD_TEST_PATH=""

echo "REPO_ROOT is: {$REPO_ROOT}"

. "$TEST_GEN_DIR/lib/orchestration/setup_tree.sh"

generate_test() {
  printf "\n======================\n"
  printf "*\n* generating test from %s\n*\n" "$TEST_CONFIG"
  printf "======================\n"
  # Capture output and extract shellspec_name
  gen_output=$("$TEST_GEN_DIR/lib/ruby/test_generator.rb" "$TEST_CONFIG")
  gen_result=$?

  echo "$gen_output"

  if [ $gen_result -ne 0 ]; then
    return 1
  fi

  # Extract shellspec_name from output
  GENERATED_TEST_NAME=$(echo "$gen_output" | grep "^__SHELLSPEC_NAME__:" | cut -d: -f2)

  if [ -z "$GENERATED_TEST_NAME" ]; then
    printf "\n ❌ Failed to extract shellspec_name from generator output\n"
    return 1
  fi

  # Set paths now that we have the test name
  GENERATED_TEST_PATH="$TEST_GEN_DIR/tmp/$GENERATED_TEST_NAME.sh"
  PROD_TEST_PATH="$PROD_TEST_DIR/$GENERATED_TEST_NAME.sh"

  printf "\n*\n* Generated test: %s\n*\n" "$GENERATED_TEST_NAME"
}

confirm_generated_test_works() {
   printf "\n*\n* confirming test works %s.sh\n*\n" "$GENERATED_TEST_NAME"

   if shellspec "$GENERATED_TEST_PATH"; then
      echo "test confirmed, copy to production"
      rm -f "$PROD_TEST_PATH"
      mv "$GENERATED_TEST_PATH" "$PROD_TEST_DIR"
   else
       return 1
   fi
}

## generate and confirm test

# setup zfs pools to desired state before running test
 if ! setup_tree "$SETUP_TREE_SPECS"; then
      printf "\n ❌ Failed to setup ZFS tree with specs %s\n!" "$SETUP_TREE_SPECS"
      exit 1
 fi

# generate the test
if ! generate_test; then
    printf "\n ❌ Test generation failed for %s!\n" "$TEST_CONFIG"
    exit 1
fi

# setup zfs pools to desired state again before running generated test
 if ! setup_tree "$SETUP_TREE_SPECS"; then
      printf "\n ❌ Failed to setup ZFS tree for testing generated tree with specs %s\n!" "$SETUP_TREE_SPECS"
      exit 1
 fi

# confirm generated test works
if ! confirm_generated_test_works; then
      printf "\n ❌ Generated test failed %s\n!" "$SETUP_TREE_SPECS"
      exit 1
fi

# good test generated and copied to prod
printf "\n ✅ Success, Generated test copied to production %s\n\n" "$PROD_TEST_PATH"
set +x
