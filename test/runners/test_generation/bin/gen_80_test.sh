#!/usr/bin/env bash

REPO_ROOT=$(git rev-parse --show-toplevel)
TEST_GEN_DIR="${REPO_ROOT}/test/runners/test_generation"

TEST_DEFS="${TEST_GEN_DIR}/config/test_defs"
RUBY_DIR="${TEST_GEN_DIR}/lib/ruby"
TEST_YML="${TEST_DEFS}/0100_standard/080_zelta_policy_test.yml"

cd "$REPO_ROOT" || { echo "cannot cd to repo root $REPO_ROOT"; exit 1; }

PROD_SPEC_DIR="${REPO_ROOT}/test/spec/0100_standard"
SHELLSPEC_CLEANUP=--setup="shellspec --tag=testgen-destroy"
SHELLSPEC_SETUP=--setup="shellspec --tag=install,initialize,standard:22,standard:30,standard:40,standard:50,standard:60,standard:70"
VERIFIED_DIR="--verified-dir=$PROD_SPEC_DIR"

ruby "$RUBY_DIR/run_test_generator.rb" "$VERIFIED_DIR" "$SHELLSPEC_CLEANUP" "$SHELLSPEC_SETUP" "$TEST_YML"
