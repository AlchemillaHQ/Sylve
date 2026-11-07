# Auto-generated ShellSpec test file
# Generated at: 2026-07-06 14:58:05 -0400
# Source: 020_prune_options_spec
# WARNING: This file was automatically generated. Manual edits may be lost.

output_for_prune_30x1_day_52x1_week_1yr() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(printf '%s' "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    # check line against expected output
    case "$normalized" in
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-06-14_03.00.00%zelta_2026-06-14_09.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-06-13_03.00.00%zelta_2026-06-13_15.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-06-12_03.00.00%zelta_2026-06-12_15.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-06-11_03.00.00%zelta_2026-06-11_15.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-06-10_03.00.00%zelta_2026-06-10_15.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-06-09_03.00.00%zelta_2026-06-09_15.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-06-08_03.00.00%zelta_2026-06-08_15.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-05-09_21.00.00%zelta_2026-05-12_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-05-03_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-04-27_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-04-18_21.00.00%zelta_2026-04-21_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-04-12_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-04-06_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-03-28_21.00.00%zelta_2026-03-31_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-03-22_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-03-16_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-03-07_21.00.00%zelta_2026-03-10_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-03-01_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-02-23_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-02-14_21.00.00%zelta_2026-02-17_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-02-08_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-02-02_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-01-24_21.00.00%zelta_2026-01-27_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-01-18_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-01-12_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-01-03_21.00.00%zelta_2026-01-06_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2025-12-28_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2025-12-22_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2025-12-16_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2024-05-21_21.00.00%zelta_2025-04-22_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2023-07-04_21.00.00%zelta_2024-04-23_21.00.00")
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

Describe 'Test prune options' prune-scenario:20
  Include "${SHELLSPEC_HELPERDIR}/golden_pool_helper.sh"
  
  EXPECTED_SNAPSHOTS=168
  PRUNE_HOOK_DEBUG_LOG="/tmp/zelta_sandbox_prune_hooks_log.txt"
  
  # don't use ShellSpec Before/After All hooks, they are executed even when this spec isn't selected
  # before / after hooks are not It clauses 'restores golden pools' and 'removes golden pools' respectively
  # TODO: after testing redirect stdout to /dev/null, stderr output is intended to fail the tests
  setup_pools() { make_golden_pools > $PRUNE_HOOK_DEBUG_LOG; }
  teardown_pools() { teardown_golden_pools >> $PRUNE_HOOK_DEBUG_LOG; }
  
  snapshot_count() {
    out=$(tgt_exec zfs list -r -t snapshot "$1") || return
    printf '%s\n' "$out" | wc
  }

  Skip if 'SANDBOX_ZELTA_SRC_EP undefined' test -z "$SANDBOX_ZELTA_SRC_EP"
  Skip if 'SANDBOX_ZELTA_TGT_EP undefined' test -z "$SANDBOX_ZELTA_TGT_EP"

  It "restores golden pools" prune-scenario:restore
    When call setup_pools
    The status should be success
  End

  It "${SANDBOX_ZELTA_SRC_EP} has $EXPECTED_SNAPSHOTS snapshots"
    When call snapshot_count "$SANDBOX_ZELTA_TGT_DS"
    The output should include "$EXPECTED_SNAPSHOTS"
    The status should be success
  End

  It "${SANDBOX_ZELTA_TGT_EP} has $EXPECTED_SNAPSHOTS snapshots"
    When call snapshot_count "$SANDBOX_ZELTA_TGT_DS"
    The output should include "$EXPECTED_SNAPSHOTS"
    The status should be success
  End

  It "prune multiple grid - run zelta prune --prune-grid='30x1 day, 52x1 week, 1 year'  \"$SANDBOX_ZELTA_SRC_EP\"  \"$SANDBOX_ZELTA_TGT_EP\""
    When run zelta prune --prune-grid='30x1 day, 52x1 week, 1 year'  "$SANDBOX_ZELTA_SRC_EP"  "$SANDBOX_ZELTA_TGT_EP"
    The output should satisfy output_for_prune_30x1_day_52x1_week_1yr
    The status should be success
  End

  It "teardown golden pools" prune-scenario:teardown
    When call teardown_pools
    The status should be success
  End

End
