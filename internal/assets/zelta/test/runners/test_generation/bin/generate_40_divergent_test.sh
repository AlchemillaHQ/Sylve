#!/usr/bin/env bash

TEST_DEF=040_zelta_tests.yml
SETUP_SPECS="test/01*_spec.sh|test/01*_spec.sh|test/02*_spec.sh"

./generate_test.sh $TEST_DEF "$SETUP_SPECS"