#!/usr/bin/env bash

REPO_ROOT=$(git rev-parse --show-toplevel)
TEST_GEN_DIR="${REPO_ROOT}/test/runners/test_generation"

TEST_DEFS="${TEST_GEN_DIR}/config/test_defs"
RUBY_DIR="${TEST_GEN_DIR}/lib/ruby"
TEST_YML="${TEST_DEFS}/0200_prune/020_prune_grid_test.yml"

cd "$REPO_ROOT" || { echo "cannot cd to repo root $REPO_ROOT"; exit 1; }

PROD_SPEC_DIR="${REPO_ROOT}/test/spec/0200_prune"
SHELLSPEC_CLEANUP='--setup-shellspec="shellspec --tag=testgen-destroy"'
SHELLSPEC_SETUP='--setup-shellspec="shellspec --tag=install"'
VERIFIED_DIR="--verified-dir=$PROD_SPEC_DIR"
# VERIFIED_DIR=

#ruby "$RUBY_DIR/run_test_generator.rb" "$VERIFIED_DIR" $SHELLSPEC_CLEANUP $SHELLSPEC_SETUP "$TEST_YML"

#  --setup-shellspec="shellspec --tag=install"
#  --setup-shellspec="shellspec --tag=testgen-destroy"

args=(
    "$VERIFIED_DIR"
    --setup-shellspec="shellspec --tag=install"
    --teardown-shellspec="shellspec --tag=cleanup:install"
    "$TEST_YML"
)
set -x
ruby "$RUBY_DIR/run_test_generator.rb" "${args[@]}"
set +x