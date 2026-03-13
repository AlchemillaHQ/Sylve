#!/bin/sh
# SPDX-License-Identifier: BSD-2-Clause
#
# Copyright (c) 2025 The FreeBSD Foundation.
#
# This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
# of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
# under sponsorship from the FreeBSD Foundation.


# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TEST_GEN_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG_DIR="$TEST_GEN_DIR/config/test_defs"
GENERATE_TEST="$TEST_GEN_DIR/lib/orchestration/generate_test.sh"

# Generate tests for 40,50,60 examples

if ! "$GENERATE_TEST" \
 "$CONFIG_DIR/040_zelta_tests.yml" \
 "test/01*_spec.sh|test/01*_spec.sh|test/02*_spec.sh"; then

  printf "\n ❌ Failed to generate 040 test\n"
  exit 1
fi

if ! "$GENERATE_TEST" \
 "$CONFIG_DIR/050_zelta_revert_test.yml" \
 "test/01*_spec.sh|test/01*_spec.sh|test/02*_spec.sh|test/040_*_spec.sh"; then

  printf "\n ❌ Failed to generate 050 test\n"
  exit 1
fi


if ! "$GENERATE_TEST" \
 "$CONFIG_DIR/060_zelta_clone_test.yml" \
 "test/01*_spec.sh|test/01*_spec.sh|test/02*_spec.sh|test/040_*_spec.sh|test/050_*_spec.sh"; then

  printf "\n ❌ Failed to generate 060 test\n"
  exit 1
fi
