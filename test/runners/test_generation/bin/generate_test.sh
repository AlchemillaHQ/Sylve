#!/usr/bin/env bash

# Check if arguments were provided
if [ $# -ne 2 ]; then
    echo "Invalid number of arguments!"
    echo "Usage: $0 (test yml file) (setup specs)"
    exit 1
fi

TEST_DEF=$1
SETUP_SPECS=$2

# Get the directory where this script is located
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TEST_GEN_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG_DIR="$TEST_GEN_DIR/config/test_defs"
GENERATE_TEST="$TEST_GEN_DIR/lib/orchestration/generate_test.sh"


printf "\n***\n*** Generating test for %s\n***\n" "$CONFIG_DIR/$TEST_DEF"
if ! "$GENERATE_TEST" "$CONFIG_DIR/$TEST_DEF" "$SETUP_SPECS"; then
  printf "\n ❌ Failed to generate test for %s\n" "$TEST_DEF"
  exit 1
fi

