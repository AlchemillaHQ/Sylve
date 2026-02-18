# Auto-generated ShellSpec test file
# Generated at: 2026-02-12 13:29:24 -0500
# Source: 050_zelta_revert_spec
# WARNING: This file was automatically generated. Manual edits may be lost.

output_for_snapshot() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(echo "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$normalized" in
        "snapshot created '${SANDBOX_ZELTA_SRC_DS}@manual_test'")
        ;;
      *)
        printf "Unexpected line format: %s\n" "$line" >&2
        return 1
        ;;
    esac
  done
  return 0
}

output_for_backup_after_delta() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(echo "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$normalized" in
        "source is written; snapshotting: @zelta_"*""|\
        "syncing 12 datasets"|\
        "* sent, 22 streams received in * seconds")
        ;;
      *)
        printf "Unexpected line format: %s\n" "$line" >&2
        return 1
        ;;
    esac
  done
  return 0
}

output_for_snapshot_again() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(echo "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$normalized" in
        "snapshot created '${SANDBOX_ZELTA_SRC_DS}@another_test'")
        ;;
      *)
        printf "Unexpected line format: %s\n" "$line" >&2
        return 1
        ;;
    esac
  done
  return 0
}

output_for_revert() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(echo "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$normalized" in
        "renaming '${SANDBOX_ZELTA_SRC_DS}' to '${SANDBOX_ZELTA_SRC_DS}_manual_test'"|\
        "cloned 12/12 datasets to ${SANDBOX_ZELTA_SRC_DS}"|\
        "snapshotting: @zelta_"*""|\
        "to retain replica history, run: zelta rotate '${SANDBOX_ZELTA_SRC_DS}' 'TARGET'")
        ;;
      *)
        printf "Unexpected line format: %s\n" "$line" >&2
        return 1
        ;;
    esac
  done
  return 0
}

output_for_rotate_after_revert() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(echo "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$normalized" in
        "renaming '${SANDBOX_ZELTA_TGT_DS}' to '${SANDBOX_ZELTA_TGT_DS}_zelta_"*"'"|\
        "to ensure target is up-to-date, run: zelta backup ${SANDBOX_ZELTA_SRC_EP} ${SANDBOX_ZELTA_TGT_EP}"|\
        "no source: ${SANDBOX_ZELTA_TGT_DS}/sub5"|\
        "no source: ${SANDBOX_ZELTA_TGT_DS}/sub5/child1"|\
        "* sent, 10 streams received in * seconds")
        ;;
      *)
        printf "Unexpected line format: %s\n" "$line" >&2
        return 1
        ;;
    esac
  done
  return 0
}

Describe 'Test revert'
  Skip if 'SANDBOX_ZELTA_SRC_DS undefined' test -z "$SANDBOX_ZELTA_SRC_DS"

  It "take a snapshot of tree before changes - zelta snapshot --snap-name \"manual_test\" \"$SANDBOX_ZELTA_SRC_EP\""
    When call zelta snapshot --snap-name "manual_test" "$SANDBOX_ZELTA_SRC_EP"
    The output should satisfy output_for_snapshot
    The status should be success
  End

  It "add and remove src datasets - add_tree_delta"
    When call add_tree_delta
    The status should be success
  End

  It "backup after deltas - zelta backup \"$SANDBOX_ZELTA_SRC_EP\" \"$SANDBOX_ZELTA_TGT_EP\""
    When call zelta backup "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
    The output should satisfy output_for_backup_after_delta
    The status should be success
  End

  It "take a snapshot of tree after changes - zelta snapshot --snap-name \"another_test\" \"$SANDBOX_ZELTA_SRC_EP\""
    When call zelta snapshot --snap-name "another_test" "$SANDBOX_ZELTA_SRC_EP"
    The output should satisfy output_for_snapshot_again
    The status should be success
  End

  It "revert to last snapshot - zelta revert \"$SANDBOX_ZELTA_SRC_EP\"@manual_test"
    When call zelta revert "$SANDBOX_ZELTA_SRC_EP"@manual_test
    The output should satisfy output_for_revert
    The error should equal "warning: unexpected 'zfs clone' output: filesystem successfully created, but it may only be mounted by root
warning: unexpected 'zfs clone' output: filesystem successfully created, but it may only be mounted by root
warning: unexpected 'zfs clone' output: filesystem successfully created, but it may only be mounted by root
warning: unexpected 'zfs clone' output: filesystem successfully created, but it may only be mounted by root
warning: unexpected 'zfs clone' output: filesystem successfully created, but it may only be mounted by root
warning: unexpected 'zfs clone' output: filesystem successfully created, but it may only be mounted by root
warning: unexpected 'zfs clone' output: filesystem successfully created, but it may only be mounted by root
warning: unexpected 'zfs clone' output: filesystem successfully created, but it may only be mounted by root
warning: unexpected 'zfs clone' output: filesystem successfully created, but it may only be mounted by root
warning: unexpected 'zfs clone' output: cannot open '${SANDBOX_ZELTA_SRC_DS}_manual_test/sub5@manual_test': dataset does not exist
warning: unexpected 'zfs clone' output: cannot open '${SANDBOX_ZELTA_SRC_DS}_manual_test/sub5/child1@manual_test': dataset does not exist"
    The status should be success
  End

  It "rotates after divergence - zelta rotate \"$SANDBOX_ZELTA_SRC_EP\" \"$SANDBOX_ZELTA_TGT_EP\""
    When call zelta rotate "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
    The output should satisfy output_for_rotate_after_revert
    The status should be success
  End

End
