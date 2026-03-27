# Auto-generated ShellSpec test file
# Generated at: 2026-03-15 03:00:54 -0400
# Source: 060_zelta_clone_spec
# WARNING: This file was automatically generated. Manual edits may be lost.

output_for_zfs_list_for_clone() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(printf '%s' "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$normalized" in
        "NAME ORIGIN"|\
        "${SANDBOX_ZELTA_SRC_DS}/copy_of_sub2 ${SANDBOX_ZELTA_SRC_DS}/sub2@zelta_"*""|\
        "${SANDBOX_ZELTA_SRC_DS}/copy_of_sub2/orphan ${SANDBOX_ZELTA_SRC_DS}/sub2/orphan@zelta_"*"")
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

Describe 'Test clone'
  Skip if 'SANDBOX_ZELTA_SRC_DS undefined' test -z "$SANDBOX_ZELTA_SRC_DS"

  It "zelta clone sub2 (ignore warnings) - zelta clone -qq \"$SANDBOX_ZELTA_SRC_EP/sub2\" \"$SANDBOX_ZELTA_SRC_EP/copy_of_sub2\""
    When call zelta clone -qq "$SANDBOX_ZELTA_SRC_EP/sub2" "$SANDBOX_ZELTA_SRC_EP/copy_of_sub2"
    The status should be success
  End

  It "verifies the clone - src_exec zfs list -ro name,origin $SANDBOX_ZELTA_SRC_DS/copy_of_sub2"
    When call src_exec zfs list -ro name,origin $SANDBOX_ZELTA_SRC_DS/copy_of_sub2
    The output should satisfy output_for_zfs_list_for_clone
    The status should be success
  End

End
