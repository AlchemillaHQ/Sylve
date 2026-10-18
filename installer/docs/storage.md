# Storage and encryption

**Back up the selected storage before installing.** Selected whole disks are
erased. Selected existing ZFS partitions also receive new pool data; keeping their
partition boundaries does not preserve their contents. Failed installations do
not roll back writes.

The installer offers guided ZFS and UFS layouts. The disk manager can inspect,
wipe and prepare disks, but there is no manual mountpoint editor or general
dual-boot workflow. The running installer media cannot be selected as a target.

## Capacity

At least 8 GiB of root/data capacity must remain after boot/EFI, swap, encryption
overhead and RAID redundancy. This is not a promise of 8 GiB free after installing
packages. Capacity is checked at installation start and again before partitioning.

RAID-Z capacity uses the smallest member and parity count. RAID-10 uses adjacent
mirror pairs in the selected order. Unknown disk identity or partition geometry
is rejected rather than guessed.

## ZFS

A pool can contain multiple data vdevs:

| Type | Minimum members |
| --- | --- |
| Stripe | 1 |
| Mirror | 2 |
| RAID-Z1 | 3 |
| RAID-Z2 | 4 |
| RAID-Z3 | 5 |
| RAID-10 | 4, with an even member count |

Members may be whole disks or existing `freebsd-zfs` partitions. At least one
whole disk is required for BIOS boot code and the EFI system partition. A disk
and one of its partitions cannot both join the pool.

Each whole disk receives GPT boot code, a 260 MiB EFI partition, optional swap
and the remaining data partition. GELI installations also reserve 2 GiB per whole
disk for an unencrypted `bootpool` carrying `/boot`; multiple bootpool members are
mirrored. Root/data providers use the boot passphrase, with no keyfile on the
bootpool.

**Existing partition members are not GELI-encrypted by the installer**, even when
encryption is enabled for the whole disks. They receive neither swap nor new boot
code. Do not use them when the entire pool must be encrypted.

The pool defaults to `zroot`, `lz4` compression and force-4K alignment. `bootpool`
is reserved. Additional pool properties are supported, except `altroot` and
`cachefile`, which the installer manages.

Installed ZFS systems disable GEOM disk-ident and GPT-ID aliases, matching
`bsdinstall`. ZFS normally displays provider names such as `ada0p4` or `vtbd0p4`
instead of `diskid/DISK-...` names. Pool identity still comes from ZFS metadata,
not those device names.

## UFS

UFS targets one entire disk or one contiguous GPT free extent. Free-space mode
keeps existing partitions and creates the required boot/EFI, swap and root
partitions in that extent. Separate gaps are not added together. Conflicting
labels or insufficient contiguous space stop installation before partitioning.
Existing boot/EFI partitions can be reused and their boot files updated; this
does not preserve another operating system's boot setup. GELI is optional for
the root filesystem.

## Swap

Swap size `0` disables swap. Nonzero allocations round up to MiB boundaries. ZFS
creates one swap partition per selected whole disk, not per existing partition
member; UFS creates one on its target disk.

When GELI is selected, configured swap uses AES-XTS-256 with fresh temporary keys
at each activation. No extra passphrase prompt or stored swap key is needed.
Without GELI, swap is unencrypted. Enabling encryption does not securely erase
plaintext swap left by an older installation. Crash dumps are disabled, and
temporary keys mean swap contents cannot be recovered after reboot.

An encrypted swap entry looks like:

```fstab
/dev/gpt/swap0.eli none swap sw,ealgo=AES-XTS,keylen=256 0 0
```

On the installed system, inspect swap from a root console with:

```sh
cat /etc/fstab
swapinfo -k
geli list
```

Encrypted swap devices should end in `.eli`; their GELI providers should report
`ONETIME` and AES-XTS with a 256-bit key. The root provider is not a one-time swap
provider. FreeBSD's native [fstab(5)](https://man.freebsd.org/cgi/man.cgi?query=fstab&sektion=5)
and `swapon` handling activate these entries; there is no custom swap service.

## Failed installations and pool conflicts

An interrupted installation cannot resume automatically. Collect the
[installation logs](boot.md#logs) before rebooting or retrying, then review the
target selection again.

Retry cleanup may destroy imported root/bootpool leftovers only when all their
devices are selected, including logs, cache, spares, special and dedup devices.
Selecting a partition does not authorize its siblings. Pool identity and device
scope are rechecked before cleanup; unknown or changed topology stops the operation.

A pool name alone is not permission to destroy it. An unrelated bootpool is left
alone for an unencrypted install; an encrypted install rejects the conflict.
Resolve conflicts only after identifying the pool and the data it contains, not
by blindly destroying it. Do not change storage from another session while an
installation is running; these checks cannot lock out external root-level tools.

## API storage objects

Use one of these objects as an install plan's `storage` field. Replace the disk
placeholders with names from authenticated `GET /api/disks`, without `/dev/`.
See [Unattended installation](autoinstall.md#plan) for a complete plan.

```json
{
  "mode": "zfs",
  "zfs": {
    "name": "zroot",
    "vdevs": [{ "type": "mirror", "disks": ["TARGET_DISK_1", "TARGET_DISK_2"] }],
    "swap": "2g",
    "encrypt": false
  }
}
```

```json
{
  "mode": "ufs",
  "ufs": {
    "disk": "TARGET_DISK",
    "target": "entire",
    "swap": "2g",
    "encrypt": false
  }
}
```

UFS `target` is `entire` or `free`; omission defaults to `entire`. For either
filesystem, enabling `encrypt` also requires a `passphrase` of at least eight
characters. Use a strong passphrase and keep it somewhere safe.

## Dataset layout

The ZFS pool root uses `mountpoint=/<pool>` and `canmount=off`, allowing Sylve's
workload datasets to inherit usable paths without mounting over the OS.
`ROOT/default` is the boot environment mounted at `/`. The `ROOT`, `usr` and `var`
containers use `mountpoint=none` and `canmount=off`; separate filesystems cover
`/tmp`, `/var/log`, `/var/db`, `/usr/ports` and `/usr/src`.

The [engine plan types](../backend/internal/engine/engine.go) and
[layout validator](../backend/internal/engine/storage_layout.go) define the
implementation.
