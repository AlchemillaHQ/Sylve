#!/bin/sh
# Usage: ./run-tests.sh [main|a|b|destructive|all]
set -eu

SPECS_DIR=test/spec

# Note: to combine tags and specs you can
# use multiple shellspec calls
#
#    SPECS_SHARED_DIR=$SPECS_DIR/shared
#    shellspec --tag install,initialize
#    shellspec $SPECS_DIR/0100_standard
#    shellspec --tag cleanup


target="${1:-all}"
echo "Running shellspec scenario \"$target\""

export SANDBOX_ZELTA_TMP_SUFFIX="$LOGNAME"

case "$target" in
  standard)
    shellspec --tag install,initialize,standard,cleanup
    ;;
  cleanup)
    shellspec --tag cleanup
    ;;
  pool-cleanup)
    shellspec --tag pool-cleanup
    ;;
  setup)
    shellspec --tag install,initialize
    ;;
  all)
    shellspec --tag install,initialize,standard,cleanup
    ;;
  *)
    echo "Unknown target: $target" >&2
    echo "Usage: $0 [standard|all|cleanup|pool-cleanup|setup]" >&2
    exit 1
    ;;
esac