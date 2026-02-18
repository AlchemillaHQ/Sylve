# Running tests with shellspec

- after installing `shellspec`

- define your sandbox environment variables
   - source an environment setup script 
     - modify or create a new env setup script, see [test/runners/env/test_env.sh](runners/env/test_env.sh)
   - or set the environment variables directly in your shell session
   - >NOTE: you can run a basic smoke test without setting any environment variables
- run `shellspec`
   - cd to the repo root for `zelta`
   - `shellspec`

### If testing remotely:
- Setup your test user on your source and target machines
  - update sudoers, for example on Linux
    - create /etc/sudoers.d/zelta-tester
    ```
    # Allow (mytestuser) to run ZFS commands without password for zelta testing
    # NOTE: This is for test environments only - DO NOT use in production
    # CAUTION: The wildcards show intent only, with globbing other commands may be allowed as well
    (mytestuser) ALL=(ALL) NOPASSWD: /usr/bin/dd *, /usr/bin/rm -f /tmp/*, /usr/bin/truncate *, /usr/sbin/zpool *, /usr/sbin/zfs *    
    ```
   - TODO: confirm if usr/bin/mount *, /usr/bin/mkdir * are needed
 
  - setup zfs allow on your source and target machines will be set up automatically for your test pools
