# Auto-generated ShellSpec test file
# Generated at: 2026-02-12 13:28:34 -0500
# Source: 040_zelta_tests_spec
# WARNING: This file was automatically generated. Manual edits may be lost.

output_for_match_after_divergence() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(echo "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$normalized" in
        "DS_SUFFIX MATCH SRC_LAST TGT_LAST INFO"|\
        "[treetop] @start @start @start up-to-date"|\
        "/sub1 @start @start @start up-to-date"|\
        "/sub1/child - - - syncable (full)"|\
        "/sub1/kid - - - no source (target only)"|\
        "/sub2 - @two @two blocked sync: target diverged"|\
        "/sub2/orphan @start @start @start up-to-date"|\
        "/sub3 @start @two @start syncable (incremental)"|\
        "/sub3/space name @start @start @blocker blocked sync: target diverged"|\
        "/sub4 @start @start @start up-to-date"|\
        "/sub4/encrypted @start @start @start up-to-date"|\
        "/sub4/zvol - @start - blocked sync: no target snapshots"|\
        "5 up-to-date, 2 syncable, 4 blocked"|\
        "11 total datasets compared")
        ;;
      *)
        printf "Unexpected line format: %s\n" "$line" >&2
        return 1
        ;;
    esac
  done
  return 0
}

output_for_rotate_after_divergence() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(echo "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$normalized" in
        "source is written; snapshotting: @zelta_"*""|\
        "renaming '${SANDBOX_ZELTA_TGT_DS}' to '${SANDBOX_ZELTA_TGT_DS}_start'"|\
        "to ensure target is up-to-date, run: zelta backup ${SANDBOX_ZELTA_SRC_EP} ${SANDBOX_ZELTA_TGT_EP}"|\
        "no source: ${SANDBOX_ZELTA_TGT_DS}/sub1/kid"|\
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

output_for_match_after_rotate() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(echo "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$normalized" in
        "DS_SUFFIX MATCH SRC_LAST TGT_LAST INFO"|\
        "[treetop] @zelta_"*" @zelta_"*" @zelta_"*" up-to-date"|\
        "/sub1 @zelta_"*" @zelta_"*" @zelta_"*" up-to-date"|\
        "/sub1/child @zelta_"*" @zelta_"*" @zelta_"*" up-to-date"|\
        "/sub2 @two @zelta_"*" @two syncable (incremental)"|\
        "/sub2/orphan @zelta_"*" @zelta_"*" @zelta_"*" up-to-date"|\
        "/sub3 @two @zelta_"*" @two syncable (incremental)"|\
        "/sub3/space name @zelta_"*" @zelta_"*" @zelta_"*" up-to-date"|\
        "/sub4 @zelta_"*" @zelta_"*" @zelta_"*" up-to-date"|\
        "/sub4/encrypted @zelta_"*" @zelta_"*" @zelta_"*" up-to-date"|\
        "/sub4/zvol @start @zelta_"*" @start syncable (incremental)"|\
        "7 up-to-date, 3 syncable"|\
        "10 total datasets compared")
        ;;
      *)
        printf "Unexpected line format: %s\n" "$line" >&2
        return 1
        ;;
    esac
  done
  return 0
}

output_for_backup_after_rotate() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(echo "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$normalized" in
        "syncing 10 datasets"|\
        "10 datasets up-to-date"|\
        "* sent, 3 streams received in * seconds")
        ;;
      *)
        printf "Unexpected line format: %s\n" "$line" >&2
        return 1
        ;;
    esac
  done
  return 0
}

Describe 'Run zelta commands on divergent tree'
  Skip if 'SANDBOX_ZELTA_SRC_DS undefined' test -z "$SANDBOX_ZELTA_SRC_DS"
  Skip if 'SANDBOX_ZELTA_TGT_DS undefined' test -z "$SANDBOX_ZELTA_TGT_DS"

  It "show divergence - zelta match \"$SANDBOX_ZELTA_SRC_EP\" \"$SANDBOX_ZELTA_TGT_EP\""
    When call zelta match "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
    The output should satisfy output_for_match_after_divergence
    The status should be success
  End

  It "rotates after divergence - zelta rotate \"$SANDBOX_ZELTA_SRC_EP\" \"$SANDBOX_ZELTA_TGT_EP\""
    When call zelta rotate "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
    The output should satisfy output_for_rotate_after_divergence
    The error should equal "warning: insufficient snapshots; performing full backup for 3 datasets"
    The status should be success
  End

  It "match after rotate - zelta match \"$SANDBOX_ZELTA_SRC_EP\" \"$SANDBOX_ZELTA_TGT_EP\""
    When call zelta match "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
    The output should satisfy output_for_match_after_rotate
    The status should be success
  End

  It "backup after rotate - zelta backup \"$SANDBOX_ZELTA_SRC_EP\" \"$SANDBOX_ZELTA_TGT_EP\""
    When call zelta backup "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
    The output should satisfy output_for_backup_after_rotate
    The status should be success
  End

End
