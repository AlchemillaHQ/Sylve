# Installer

This directory contains Sylve's live-media installer: a local kiosk wizard that
installs FreeBSD from pkgbase and Sylve, with guided ZFS or UFS storage and optional
GELI encryption. Hybrid ISOs for 15-STABLE and 16-CURRENT boot on amd64 BIOS/UEFI
systems, from optical media or USB, and include an offline payload fallback.

For Sylve's features, requirements, package installation, contribution guidance
and license, see the [repository README](../README.md).

## Build

Run from the repository root with Go 1.25 or newer, Node.js 24 and npm:

```sh
make installer-ui
make installer-backend
```

Installer targets are defined in the root `Makefile`; run `make installer-help`
for the full list. They are separate from Sylve's normal `build` and `clean`.
`INSTALLER_VERSION` overrides the daemon version, and `INSTALLER_BIN_DIR` overrides
the default `installer/out` binary directory.
`INSTALLER_SYLVE_COMMIT` pins installation to the matching verified bundled Sylve
binary; without it, source builds retain rolling `tip` downloads with an offline
fallback.

Build the UI first: it is embedded in `installer/out/sylve-installerd`. Rebuild the
daemon after UI changes; editing Svelte source alone does not update an image.

Run backend checks with `make installer-test installer-vet`.

Stage and assemble media on FreeBSD as root, using explicit working directories:

```sh
sudo env STAGE_DIR=/path/to/stage sh installer/build/stage-live.sh
sudo sh installer/build/build-image.sh /path/to/stage /path/to/output
```

The default output is `freebsd-15-STABLE-sylve-installer-amd64.iso`, with a local
SHA-256 sidecar. For 16-CURRENT, use separate stage/output directories and pass
`FBSD_MAJOR=16 FBSD_BRANCH=main FBSD_ABI=FreeBSD:16:amd64` through `sudo env` to
both scripts. Its output is `freebsd-16-CURRENT-sylve-installer-amd64.iso`.
The target branch and repositories are configured in
[`build/pkgbase.conf`](build/pkgbase.conf), with environment overrides for
`FBSD_MAJOR`, `FBSD_BRANCH`, `FBSD_ABI`, `PKGBASE_REPO` and `PORTS_REPO`.
Staging includes the offline payload unless `OFFLINE_PAYLOAD=0` is set.

The daemon/UI can be built on Linux or FreeBSD. Image assembly requires FreeBSD's
`makefs`, `mkuzip`, `mkimg`, `etdump` and `fsck_msdosfs`; it is not part of the
normal Sylve application build. Allow space for the staged root, filesystem image
and ISO; assembly checks available scratch space before starting.

The native scripts have separate roles so a staged root can be reused:

- `stage-live.sh` installs packages and applies the live overlay.
- `fetch-payload.sh` refreshes the offline payload; staging calls it by default.
- `build-image.sh` assembles the staged root into bootable media.

The ISO has a read-only optical root and a Zstandard-compressed UFS `/usr`, mounted
on demand rather than preloaded into RAM. Configuration, runtime state and the
kiosk profile use writable RAM filesystems; the offline payload stays read-only
and is not copied into RAM overlays. A complete, matching
stage and offline package repository can be reused with
`STAGE_PACKAGES=0 PAYLOAD_PACKAGES=0`; fresh stages
must use the defaults. `SYLVE_PAYLOAD_DIR` supplies a prebuilt `sylve-amd64`,
`sylve-version.json` and `SHA256SUMS` instead of downloading the rolling release.
Downloading the rolling payload requires `jq` on the FreeBSD builder to read
GitHub's asset digest. CI supplies its own verified payload and does not need it.

The optional [artwork generator](docs/boot.md#artwork)
uses Node.js, not Python, and is not required for image assembly.

## Release images

The `installer.yaml` workflow runs lightweight checks on PRs/pushes and is called
by the tip and `v*` release pipelines to build images. Each release gets both
15-STABLE and 16-CURRENT amd64 hybrid BIOS/UEFI `.iso` assets. GitHub exposes each
asset's SHA-256 digest, so no separate
checksum files are uploaded. CI and the offline bundle retain internal
verification data. Tagged-release images are attached to
the existing draft; publish it only after the release workflow succeeds.
Attach the ISO as virtual optical media, burn it to a DVD, or write it directly
to a USB device. No decompression or image conversion is needed.

The UI and daemon build on Linux. A GitHub-hosted FreeBSD VM handles package
staging and image assembly, reusing a prepared package stage rather than
reinstalling packages for every release. Each OS version has a separate stage
cache, which refreshes weekly and
when package policy, staging scripts or the live overlay change. UI output and
Go dependencies are cached separately; image artifacts are not compressed twice.
The first cold build still needs to download and stage packages.

Each release image bundles the already-built Sylve binary from that same pipeline
and pins installation to its source commit, including when online. It does not
fetch a newer `tip` or rebuild Sylve inside FreeBSD.

## Use

Boot the image and select **Start installer**. The normal menu countdown is
15 seconds; recovery, boot options and the loader prompt remain available.
The wizard configures storage, system credentials and the installed network.
Selected whole disks are erased; inspect the inventory and summary carefully.

To continue remotely, open the URL shown beside the kiosk QR code and enter its
live-session access password. Headless systems print access details on the text
console. Use a trusted provisioning network or encrypted tunnel: the installer
uses HTTP. Live SSH and in-guest VNC are disabled; use a physical, IPMI or
hypervisor console for recovery and GELI unlock.

After installation, remove the media and boot the target. Use the administrator
password chosen during installation, not the default credentials of a fresh
package installation. Sylve applies the target network on first startup; see
[First boot and networking](docs/integration.md).

## Installer references

- [Boot and troubleshooting](docs/boot.md)
- [Checks, live networking and remote access](docs/checks.md)
- [Storage, encryption and safety](docs/storage.md)
- [First boot and networking](docs/integration.md)
- [Unattended provisioning](docs/autoinstall.md)

Unattended plans remain unsigned, and interrupted installations do not have
durable automatic resume.
