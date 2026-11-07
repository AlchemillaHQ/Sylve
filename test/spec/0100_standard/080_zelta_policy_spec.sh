# Auto-generated ShellSpec test file
# Generated at: 2026-07-03 03:18:32 -0400
# Source: 080_zelta_policy_spec
# WARNING: This file was automatically generated. Manual edits may be lost.

output_for_policy_check() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(printf '%s' "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    # check line against expected output
    case "$normalized" in
        "[BACKUP_SITE: ${SANDBOX_ZELTA_TGT_EP}] ${SANDBOX_ZELTA_SRC_EP}: 12 datasets up-to-date")
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

Describe 'Test zelta policy' standard:80
  Skip if 'SANDBOX_ZELTA_SRC_DS undefined' test -z "$SANDBOX_ZELTA_SRC_DS"
  Skip if 'SANDBOX_ZELTA_TGT_DS undefined' test -z "$SANDBOX_ZELTA_TGT_DS"

  It "generate zelta policy - run ./test/runners/test_generation/bin/generate_zelta_policy_config.sh"
    When run ./test/runners/test_generation/bin/generate_zelta_policy_config.sh
    The status should be success
  End

  It "test zelta policy - run zelta policy -C ./test/runners/test_generation/config/zelta_test_policy.conf"
    When run zelta policy -C ./test/runners/test_generation/config/zelta_test_policy.conf
    The output should satisfy output_for_policy_check
    The status should be success
  End

End
