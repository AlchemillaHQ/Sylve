#!/usr/bin/env bash

# used to debug test generation
# use default for verified dir so production spec is not over written on success

REPO_ROOT=$(git rev-parse --show-toplevel)
TEST_GEN_DIR="${REPO_ROOT}/test/runners/test_generation"

TEST_DEFS="${TEST_GEN_DIR}/config/test_defs"
RUBY_DIR="${TEST_GEN_DIR}/lib/ruby"
TEST_YML="${TEST_DEFS}/0100_standard/040_zelta_tests.yml"

cd "$REPO_ROOT" || { echo "cannot cd to repo root $REPO_ROOT"; exit 1; }

PROD_SPEC_DIR="${REPO_ROOT}/test/spec/0100_standard"
SHELLSPEC_CLEANUP=--setup="shellspec --tag=testgen-destroy"
SHELLSPEC_SETUP=--setup="shellspec --tag=install,initialize,standard:22,standard:30"
#VERIFIED_DIR="--verified-dir=$PROD_SPEC_DIR"

# verified dir omitted from args
ruby "$RUBY_DIR/run_test_generator.rb" "$SHELLSPEC_CLEANUP" "$SHELLSPEC_SETUP" "$TEST_YML"
