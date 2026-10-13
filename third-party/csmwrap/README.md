# CSMWrap sources

`source/` is a pinned Git submodule; upstream pins its seven dependencies.
No upstream release binaries are used.

## Build

Install the tools below, then initialize the sources and build:

```sh
git submodule update --init --recursive -- third-party/csmwrap/source
make csmwrap
```

After checkout, builds are offline and leave the submodules unchanged. Only
tracked source files are copied into a temporary build tree; SeaBIOS `.version`
metadata is generated there. Git metadata and existing build outputs are excluded.
Build orchestration, bundle verification, and staging use shell and `jq`.
Python 3 is needed only by upstream SeaBIOS, not for verification or staging.
GitHub source archives do not include submodules.

Linux:
```sh
sudo apt-get install git gcc libc6-dev make binutils nasm xxd dosfstools mtools xorriso file jq python3
```
FreeBSD: `pkg install git gcc14 gmake binutils nasm xxd mtools xorriso jq python311`
(uses base `newfs_msdos`).

`.cache/csmwrap` contains the ISO, gzip embed input, manifest, and checksums.
`./scripts/build-csmwrap.sh --verify --stage` stages an existing bundle.
AMD64 build/test targets stage automatically; ARM64 does not need the helper.
CI caches one Ubuntu 24.04 build by inputs and runner image for embedding/releases.
Toolchain and FAT/ISO timestamps can change bytes; reproducibility is not promised.

`./scripts/test-csmwrap.sh` runs packaging and source-pin checks without Python,
compiling the helper, or booting guests.

To update, check out the new CSMWrap revision, update its recursive submodules,
and stage the `source/` gitlink. Dependency pins come from upstream.

## Licensing

CSMWrap is LGPL-2.1; dependencies retain their licenses and notices under `source/`.

## Runtime and limitations

`UEFI + CSMWrap (Experimental)` targets FreeBSD 15+ on amd64 and requires at least
two vCPUs (one is reserved). Graphics/device support varies by guest; serial can
help when GOP does not work. No CSMWrap guest boot tests run in CI.

The read-only helper CD uses an earlier PCI slot than managed guest media, plus
native `bootindex` ordering where supported. Boot priority is best-effort on older
hosts. Separate VARS preserve ordinary UEFI state; installer ejection keeps the helper.

## Binary updates

The current binary's embedded ISO is always used. Media preparation replaces
`<dataPath>/firmware/csmwrap/CSMWrap.iso` atomically after a basic extraction-size
check, even when the old ISO has the same size. Existing open media handles are
not modified in place. Cold starts refresh the helper automatically without
resaving Boot ROM or resetting VARS.

VMs, templates, and snapshots do not pin helper revisions. Restore, cloning,
migration, and replication use the destination binary's helper and do not require
matching ISO digests. Host architecture, CPU, and firmware requirements still
apply. Do not downgrade CSMWrap VMs to versions without `uefi_csmwrap` support.
