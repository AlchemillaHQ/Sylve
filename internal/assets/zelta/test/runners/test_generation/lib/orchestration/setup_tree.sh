# helper to setup the test zfs pools and datasets in the
# state needed for creating/generating a new test and
# for testing the same.

REPO_ROOT=${REPO_ROOT:=$(git rev-parse --show-toplevel)}
echo "REPO ROOT: $REPO_ROOT"

setup_tree() {
   setup_specs=$1
   trace_options=$2

   cd "$REPO_ROOT" || exit 1
   . ./test/test_helper.sh
   . ./test/runners/env/helpers.sh
   setup_env "1"      # setup debug environment
   clean_ds_and_pools # reset tree

    if shellspec $trace_options --pattern "$setup_specs"; then
        printf "\n ✅ setup succeeded for specs: %s\n" "$setup_specs"
    else
        printf "\n ❌ setup failed for specs: %s\n" "$setup_specs"
        exit 1
    fi
}
