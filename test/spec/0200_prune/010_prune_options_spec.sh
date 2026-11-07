# Auto-generated ShellSpec test file
# Generated at: 2026-07-10 23:42:26 -0400
# Source: 010_prune_options_spec
# WARNING: This file was automatically generated. Manual edits may be lost.

output_for_prune_newest_3_without_guard() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(printf '%s' "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    # check line against expected output
    case "$normalized" in
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2023-06-20_21.00.00%zelta_2026-06-14_03.00.00")
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

output_for_prune_all_without_guard() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(printf '%s' "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    # check line against expected output
    case "$normalized" in
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2023-06-20_21.00.00%zelta_2026-06-14_21.00.00")
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

output_for_prune_3_with_guard() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(printf '%s' "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    # check line against expected output
    case "$normalized" in
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2023-06-20_21.00.00%zelta_2026-06-13_21.00.00")
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

output_for_prune_all_synced() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(printf '%s' "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    # check line against expected output
    case "$normalized" in
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2023-06-20_21.00.00%zelta_2026-06-14_15.00.00")
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

output_for_prune_grid_weekly() {
  while IFS= read -r line; do
    # normalize whitespace, remove leading/trailing spaces
    normalized=$(printf '%s' "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    # check line against expected output
    case "$normalized" in
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-06-08_03.00.00%zelta_2026-06-14_09.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-06-01_21.00.00%zelta_2026-06-06_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-05-25_21.00.00%zelta_2026-05-30_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-05-18_21.00.00%zelta_2026-05-23_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-05-12_21.00.00%zelta_2026-05-16_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-05-06_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-04-27_21.00.00%zelta_2026-04-30_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-04-21_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-04-15_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-04-06_21.00.00%zelta_2026-04-09_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-03-31_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-03-25_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-03-16_21.00.00%zelta_2026-03-19_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-03-10_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-03-04_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-02-23_21.00.00%zelta_2026-02-26_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-02-17_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-02-11_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-02-02_21.00.00%zelta_2026-02-05_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-01-27_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-01-21_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-01-12_21.00.00%zelta_2026-01-15_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2026-01-06_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2025-12-31_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2025-12-22_21.00.00%zelta_2025-12-25_21.00.00"|\
        "${SANDBOX_ZELTA_SRC_DS}@zelta_2025-12-16_21.00.00")
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

Describe 'Test prune options' prune-scenario:10
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

  It "prune keep 3 without guard - run zelta prune --prune-num=3 --no-prune-guard \"$SANDBOX_ZELTA_SRC_EP\""
    When run zelta prune --prune-num=3 --no-prune-guard "$SANDBOX_ZELTA_SRC_EP"
    The output should satisfy output_for_prune_newest_3_without_guard
    The status should be success
  End

  It "prune all without guard - run zelta prune --prune-num=0 --no-prune-guard \"$SANDBOX_ZELTA_SRC_EP\""
    When run zelta prune --prune-num=0 --no-prune-guard "$SANDBOX_ZELTA_SRC_EP"
    The output should satisfy output_for_prune_all_without_guard
    The status should be success
  End

  It "prune 3 with guard - run zelta prune --prune-num=3 \"$SANDBOX_ZELTA_SRC_EP\" \"$SANDBOX_ZELTA_TGT_EP\""
    When run zelta prune --prune-num=3 "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
    The output should satisfy output_for_prune_3_with_guard
    The status should be success
  End

  It "prune all with unsynced guard - run zelta prune --prune-num=0 --prune-guard=unsynced \"$SANDBOX_ZELTA_SRC_EP\" \"$SANDBOX_ZELTA_TGT_EP\""
    When run zelta prune --prune-num=0 --prune-guard=unsynced "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
    The output should satisfy output_for_prune_all_synced
    The status should be success
  End

  It "prune all with unsynced guard - run zelta prune --prune-grid=1week \"$SANDBOX_ZELTA_SRC_EP\" \"$SANDBOX_ZELTA_TGT_EP\""
    When run zelta prune --prune-grid=1week "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
    The output should satisfy output_for_prune_grid_weekly
    The status should be success
  End

  It "teardown golden pools" prune-scenario:teardown
    When call teardown_pools
    The status should be success
  End

End
