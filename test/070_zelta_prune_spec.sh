# Auto-generated ShellSpec test file
# Generated at: 2026-03-15 03:01:59 -0400
# Source: 070_zelta_prune_spec
# WARNING: This file was automatically generated. Manual edits may be lost.

output_for_backup_with_snapshot() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(printf '%s' "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$normalized" in
        "snapshotting: @zelta_"*""|\
        "syncing 12 datasets"|\
        ""*" sent, 12 streams received in "*" seconds")
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

output_for_prune_check() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(printf '%s' "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$normalized" in
        "${SANDBOX_ZELTA_SRC_DS}@zelta_"*""|\
        "${SANDBOX_ZELTA_SRC_DS}/sub1@zelta_"*""|\
        "${SANDBOX_ZELTA_SRC_DS}/sub1/child@zelta_"*""|\
        "${SANDBOX_ZELTA_SRC_DS}/sub2@zelta_"*""|\
        "${SANDBOX_ZELTA_SRC_DS}/sub2/orphan@zelta_"*""|\
        "${SANDBOX_ZELTA_SRC_DS}/sub3@zelta_"*""|\
        "${SANDBOX_ZELTA_SRC_DS}/sub3/space name@zelta_"*""|\
        "${SANDBOX_ZELTA_SRC_DS}/sub4@zelta_"*""|\
        "${SANDBOX_ZELTA_SRC_DS}/sub4/encrypted@zelta_"*""|\
        "${SANDBOX_ZELTA_SRC_DS}/sub4/zvol@zelta_"*"")
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

Describe 'Test prune'
  Skip if 'SANDBOX_ZELTA_SRC_DS undefined' test -z "$SANDBOX_ZELTA_SRC_DS"
  Skip if 'SANDBOX_ZELTA_TGT_DS undefined' test -z "$SANDBOX_ZELTA_TGT_DS"

  It "backup with snapshot - zelta backup --snapshot \"$SANDBOX_ZELTA_SRC_EP\" \"$SANDBOX_ZELTA_TGT_EP\""
    When call zelta backup --snapshot "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
    The output should satisfy output_for_backup_with_snapshot
    The status should be success
  End

  It "only suggest snapshots existing on target - zelta prune --keep-snap-num=0 --keep-snap-days=0 \"$SANDBOX_ZELTA_SRC_EP\" \"$SANDBOX_ZELTA_TGT_EP\""
    When call zelta prune --keep-snap-num=0 --keep-snap-days=0 "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
    The output should satisfy output_for_prune_check
    The status should be success
  End

End
