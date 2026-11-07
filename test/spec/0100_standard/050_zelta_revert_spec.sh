# Auto-generated ShellSpec test file
# Generated at: 2026-07-03 03:13:30 -0400
# Source: 050_zelta_revert_spec
# WARNING: This file was automatically generated. Manual edits may be lost.

output_for_snapshot() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(printf '%s' "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    # check line against expected output
    case "$normalized" in
        "snapshot created '${SANDBOX_ZELTA_SRC_DS}@manual_test'")
        ;;
      *)
        printf "Unexpected line format : %s\n" "$line" >&2
        printf "Comparing to normalized: %s\n" "$normalized" >&2
        return 1
        ;;
    esac
  done
  return 0
}

output_for_backup_after_delta() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(printf '%s' "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    # check line against expected output
    case "$normalized" in
        "source is written; snapshotting: @zelta_"*""|\
        "syncing 12 datasets"|\
        ""*" sent, 22 streams received in "*" seconds")
        ;;
      *)
        printf "Unexpected line format : %s\n" "$line" >&2
        printf "Comparing to normalized: %s\n" "$normalized" >&2
        return 1
        ;;
    esac
  done
  return 0
}

output_for_snapshot_again() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(printf '%s' "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    # check line against expected output
    case "$normalized" in
        "snapshot created '${SANDBOX_ZELTA_SRC_DS}@another_test'")
        ;;
      *)
        printf "Unexpected line format : %s\n" "$line" >&2
        printf "Comparing to normalized: %s\n" "$normalized" >&2
        return 1
        ;;
    esac
  done
  return 0
}

output_for_rotate_after_revert() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(printf '%s' "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    # check line against expected output
    case "$normalized" in
        "renaming '${SANDBOX_ZELTA_TGT_DS}' to '${SANDBOX_ZELTA_TGT_DS}_zelta_"*"'"|\
        "to ensure target is up-to-date, run: zelta backup ${SANDBOX_ZELTA_SRC_EP} ${SANDBOX_ZELTA_TGT_EP}"|\
        ""*" sent, 10 streams received in "*" seconds")
        ;;
      *)
        printf "Unexpected line format : %s\n" "$line" >&2
        printf "Comparing to normalized: %s\n" "$normalized" >&2
        return 1
        ;;
    esac
  done
  return 0
}

Describe 'Test revert' standard:50
  Skip if 'SANDBOX_ZELTA_SRC_DS undefined' test -z "$SANDBOX_ZELTA_SRC_DS"

  It "take a snapshot of tree before changes - run zelta snapshot --snap-name \"manual_test\" \"$SANDBOX_ZELTA_SRC_EP\""
    When run zelta snapshot --snap-name "manual_test" "$SANDBOX_ZELTA_SRC_EP"
    The output should satisfy output_for_snapshot
    The status should be success
  End

  It "add and remove src datasets - call add_tree_delta"
    When call add_tree_delta
    The status should be success
  End

  It "backup after deltas - run zelta backup \"$SANDBOX_ZELTA_SRC_EP\" \"$SANDBOX_ZELTA_TGT_EP\""
    When run zelta backup "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
    The output should satisfy output_for_backup_after_delta
    The status should be success
  End

  It "take a snapshot of tree after changes - run zelta snapshot --snap-name \"another_test\" \"$SANDBOX_ZELTA_SRC_EP\""
    When run zelta snapshot --snap-name "another_test" "$SANDBOX_ZELTA_SRC_EP"
    The output should satisfy output_for_snapshot_again
    The status should be success
  End

  It "rejects a mixed-snapshot tree without renaming or replacing datasets"
    When call reject_mixed_revert_tree
    The error should equal "error: cannot revert tree: missing snapshot '$SANDBOX_ZELTA_SRC_DS/sub5@manual_test'; revert eligible children individually"
    The status should be success
  End

  It "explicitly preserves the newer children outside the tree to be reverted"
    When call preserve_new_revert_children
    The status should be success
  End

  It "reverts the coherent remaining tree - run zelta revert -qq \"$SANDBOX_ZELTA_SRC_EP\"@manual_test"
    When run zelta revert -qq "$SANDBOX_ZELTA_SRC_EP"@manual_test
    The status should be success
  End

  It "rotates after divergence - run zelta rotate \"$SANDBOX_ZELTA_SRC_EP\" \"$SANDBOX_ZELTA_TGT_EP\""
    When run zelta rotate "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
    The output should satisfy output_for_rotate_after_revert
    The status should be success
  End

End
