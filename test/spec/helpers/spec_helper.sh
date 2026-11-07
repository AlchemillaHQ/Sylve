if [ -z "$SHELLSPEC_HELPERDIR" ]; then
  REPO_ROOT=${REPO_ROOT:=$(git rev-parse --show-toplevel)}
  SHELLSPEC_HELPERDIR="$REPO_ROOT/test/spec/helpers"
fi

. "${SHELLSPEC_HELPERDIR}/standard_test_helper.sh"
. "${SHELLSPEC_HELPERDIR}/test_generation_helper.sh"
