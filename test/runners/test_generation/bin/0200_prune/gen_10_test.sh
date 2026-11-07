#!/usr/bin/env bash

REPO_ROOT=$(git rev-parse --show-toplevel)
TEST_GEN_DIR="${REPO_ROOT}/test/runners/test_generation"

TEST_DEFS="${TEST_GEN_DIR}/config/test_defs"
RUBY_DIR="${TEST_GEN_DIR}/lib/ruby"
TEST_YML="${TEST_DEFS}/0200_prune/010_prune_options_test.yml"

cd "$REPO_ROOT" || { echo "cannot cd to repo root $REPO_ROOT"; exit 1; }

PROD_SPEC_DIR="${REPO_ROOT}/test/spec/0200_prune"
SHELLSPEC_CLEANUP='--setup-shellspec="shellspec --tag=testgen-destroy"'
SHELLSPEC_SETUP='--setup-shellspec="shellspec --tag=install"'
VERIFIED_DIR="--verified-dir=$PROD_SPEC_DIR"

if [ -z "${SANDBOX_ZELTA_SRC_REMOTE:-}" ]; then
    echo "" >&2
    echo "*** ERROR: SANDBOX_ZELTA_SRC_REMOTE is not set" >&2
    echo "*** Test generation fidelity requires using remotes" >&2
    echo "" >&2
    exit 1
fi

if [ -z "${SANDBOX_ZELTA_TGT_REMOTE:-}" ]; then
    echo "" >&2
    echo "*** ERROR: SANDBOX_ZELTA_TGT_REMOTE is not set" >&2
    echo "*** Test generation fidelity requires using remotes" >&2
    echo "" >&2

    exit 1
fi

ZELTA_TESTGEN_DEBUG=0 # print defined environment variables on test generation

# use args[@] so that arguments are passed though correctly to ruby
args=(
    --setup-shellspec="shellspec --tag=install"
    --teardown-shellspec="shellspec --tag=cleanup:install"
    --verified-dir="$PROD_SPEC_DIR"
    "$TEST_YML"
)
set -x
ruby "$RUBY_DIR/run_test_generator.rb" "${args[@]}"
set +x