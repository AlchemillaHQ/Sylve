# helper to setup the test zfs pools and datasets in the
# state needed for creating/generating a new test and
# for testing the same.

REPO_ROOT=${REPO_ROOT:=$(git rev-parse --show-toplevel)}
echo "REPO ROOT: $REPO_ROOT"

setup_tree() {
    pattern_specs=$1
    selector_specs=$2
    trace_options=$3

    cd "$REPO_ROOT" || return 1

    if ! . ./test/test_helper.sh; then
        echo "source ./test/test_helper.sh failed"
        return 1
    fi

    if ! . ./test/runners/env/helpers.sh; then
        echo "source ./test/runners/env/helpers.sh failed"
        return 1
    fi

    if ! setup_env "1"; then
        echo "setup_env failed"
        return 1
    fi

    if ! clean_ds_and_pools; then
        echo "clean_ds_and_pools failed"
        return 1
    fi

    # Split trace_options into array (if not empty)
    trace_opts=()
    if [ -n "$trace_options" ]; then
        read -ra trace_opts <<< "$trace_options"
    fi

    # Split selector_specs into array (if not empty)
    selector_opts=()
    if [ -n "$selector_specs" ]; then
        read -ra selector_opts <<< "$selector_specs"
    fi

    cmd1=()
    if [ -n "$pattern_specs" ]; then
        cmd1=(shellspec)
        if [ ${#trace_opts[@]} -gt 0 ]; then
            cmd1+=("${trace_opts[@]}")
        fi
        cmd1+=(--pattern "$pattern_specs")
    fi

    cmd2=()
    if [ ${#selector_opts[@]} -gt 0 ]; then
        cmd2=(shellspec)
        if [ ${#trace_opts[@]} -gt 0 ]; then
            cmd2+=("${trace_opts[@]}")
        fi
        cmd2+=("${selector_opts[@]}")
    fi

    set -x
    if [ ${#cmd1[@]} -gt 0 ] && ! "${cmd1[@]}"; then
        printf "\n ❌ setup failed for command: %s\n" "${cmd1[*]}"
        set +x
        return 1
    fi

    if [ ${#cmd2[@]} -gt 0 ] && ! "${cmd2[@]}"; then
        printf "\n ❌ setup failed for command: %s\n" "${cmd2[*]}"
        set +x
        return 1
    fi
    set +x

    printf "\n ✅ setup succeeded\n"
}
