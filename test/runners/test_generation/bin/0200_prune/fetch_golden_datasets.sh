if [ -z "$SHELLSPEC_HELPERDIR" ]; then
  REPO_ROOT=${REPO_ROOT:=$(git rev-parse --show-toplevel)}
  SHELLSPEC_HELPERDIR="$REPO_ROOT/test/spec/helpers"
fi

: "${SANDBOX_ZELTA_GOLD_PRUNE_SCEN_DIR:=${SHELLSPEC_HELPERDIR}/golden/0200_prune}"

mkdir -p "$SANDBOX_ZELTA_GOLD_PRUNE_SCEN_DIR"
gh release download test-fixtures/v1 --pattern '*.img.gz' --dir "$SANDBOX_ZELTA_GOLD_PRUNE_SCEN_DIR"
gunzip "$SANDBOX_ZELTA_GOLD_PRUNE_SCEN_DIR"/*.gz
