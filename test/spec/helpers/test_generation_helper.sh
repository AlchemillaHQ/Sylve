# functions used in automatic test generation

noop() { :; }   # ignores the subject, returns 0

#: "${TESTGEN_ZELTA_WORKFLOW:?TESTGEN_ZELTA_WORKFLOW must be set}"
#
#if [ -n "$TESTGEN_ZELTA_DESTRUCTIVE" ]; then
#   # i need to bail out here
#   noop
#   # this file is sourced out do i exit
#fi

TESTGEN_ZELTA_TMP_DIR=test/runners/test_generation/tmp
#: TESTGEN_ZELTA_CAPTURE_DIR:="${SHELLSPEC_PROJECT_ROOT}/${TESTGEN_ZELTA_TMP_DIR}"
TESTGEN_ZELTA_CAPTURE_DIR="${SHELLSPEC_PROJECT_ROOT}/${TESTGEN_ZELTA_TMP_DIR}"

capture_output() {
  subdir=$1
  test_name=$2
  out_dir=$TESTGEN_ZELTA_CAPTURE_DIR/${subdir}
  mkdir -p "$out_dir"
  cp "$SHELLSPEC_STDOUT_FILE" "${out_dir}/${test_name}.stdout"
  cp "$SHELLSPEC_STDERR_FILE" "${out_dir}/${test_name}.stderr"
  printf '%s\n' "$SHELLSPEC_STATUS" > "${out_dir}/${test_name}.status"
}

testgen_record_only() {
  if [ "${TESTGEN_ZELTA_RECORD:-}" = 1 ]; then
      capture_output "$@"
      return 1
  fi
  return 0
}

