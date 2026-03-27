#!/usr/bin/env bash

TEST_DEF=080_zelta_policy_test.yml
SETUP_SPECS="test/01*_spec.sh|test/01*_spec.sh|test/02*_spec.sh|test/040_*_spec.sh|test/050_*_spec.sh|test/060_*_spec.sh|test/070_*_spec.sh"

./generate_test.sh $TEST_DEF "$SETUP_SPECS"
