# sourcing this file to setup some helpers
# for debug environment management


# create run environment
# DEBUG_MODE - not empty = setup the debug environment
#              empty     = setup the standard test environment
setup_env() {
    DEBUG_MODE=$1

    if [ -n "$DEBUG_MODE" ]; then
        . test/runners/env/setup_debug_env.sh
    else
        printf '%s\n' "--> Normal shellspec Run"
        . test/runners/env/reset_env.sh   # reset the env, use test_helper.sh version
        . test/runners/env/test_env.sh    # set dataset, pools and remote env vars
        # on normal run shellspec will automatically run test/test_helper.sh
    fi
}

# run a function, show it's status
run_it() {
    _func=$1
    #if (eval set -x; "$_func";); then  # if you want to see trace
    if (eval "$_func";); then
        printf " ✅ %s\n\n" "$_func"
    else
        printf " ❌ %s\n\n" "$_func"
        exit 1
    fi
}

# remove the zfs pools and datasets, clean slate for running shellspecs
clean_ds_and_pools() {
    echo "cleaning up, datasets and pools"
    run_it clean_src_ds
    run_it clean_tgt_ds
    run_it nuke_tgt_pool
    run_it nuke_src_pool
}
