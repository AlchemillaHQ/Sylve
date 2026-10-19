# Boot and troubleshooting

The live menu starts the installer after 15 seconds. Up/Down changes selection;
Enter activates it. Navigation or Space pauses the countdown. The menu provides
Start installer, Recovery shell, Boot options, Restart and Loader prompt.
Console, kernel and boot-environment choices are under Boot options.

Serial consoles, unsupported graphics and `loader_gfx="NO"` use FreeBSD's native
text menu. A text menu alone is not a boot failure. These settings affect only
the live media, not the installed system or its GELI unlock prompt.

Both 15-STABLE and 16-CURRENT use hybrid ISOs with BIOS and UEFI optical boot
entries and a USB-compatible partition layout. The ISO root and compressed `/usr`
are read-only; `/etc`, `/var`, `/root`, temporary files and logs are writable in RAM.
Changes to the live session do not persist after reboot. The offline payload is
kept outside those writable overlays.

For VirtualBox UEFI guests, the installer sets `hw.efi.poweroff=0` to use ACPI
rather than EFI runtime shutdown. It persists the setting in the installed
system's `/etc/sysctl.conf.local`, leaving `/etc/sysctl.conf` untouched. Detection
uses the SMBIOS product. If you're reading this.. please don't use VirtualBox
be kind to yourself, use Bhyve or proper KVM with Libvirt.

The recovery shell has the static tools in `/rescue` even before `/usr` is
mounted. If additional tools are needed there, attach `/usr.ufs.uzip` read-only
with `mdconfig -a -t vnode -o readonly -f /usr.ufs.uzip`, then mount the returned
device's `.uzip` provider on `/usr` with `mount -t ufs -o ro`.

## Startup problems

- For visible startup messages, select verbose boot in **Boot options**.
- If the kiosk cannot start, use the text console's address/password to open the
  remote installer, or choose **Recovery shell** to investigate.
- If the installer service never becomes ready, startup stays on the text console
  and shows the daemon log path.
- If remote access fails, check the [live network](checks.md#live-network-configuration).
  In a VM, the guest address may require a hypervisor port forward to be reachable.
- Keep a physical, IPMI, hypervisor or serial console available for recovery.
  The live image does not provide SSH or an in-guest VNC server.

Collect logs before rebooting or retrying a failed installation. Installer state
is held in memory and cannot resume automatically after a restart.

## Logs

| Output | Location |
| --- | --- |
| rc/DHCP startup on quiet boots | `/var/installer-boot.log`, also linked from `/var/log/installer-boot.log` |
| Kiosk/browser | `/var/log/sylve-kiosk.log` |
| Installer daemon | `/var/log/sylve_installerd.log` |
| Kernel messages | `dmesg`, `/var/run/dmesg.boot` |
| Installation progress | The wizard's Progress view or authenticated `GET /api/install` |
| Completed installation summary | `/var/log/freebsd-sylve-install.log` on the installed target; not a full progress log |

Read private logs as root. They may contain passwords or private kiosk URLs;
redact credentials and session tokens before sharing them. On a local console,
`sylve-installerd -show-access` displays the current access details again.

During quiet startup, normal rc output is logged instead of drawn over the
splash. Verbose/recovery paths retain console output. The live-only wrapper
delegates to stock `/etc/rc`; its backing log stays outside the later `/var/log`
tmpfs mount so startup output remains accessible.

## Artwork

The checked-in PNGs are used directly by image builds. The custom splash is
configured for startup only. FreeBSD's quiet shutdown may still show its native
logo, but no custom shutdown image is configured.

To regenerate a preview without replacing the PNGs, run from the repository root:

```sh
node installer/build/generate-boot-art.mjs /path/to/artwork
```

This requires Node.js, ImageMagick with SVG support, Fontconfig and Noto Sans.
Omit the output directory to replace the checked-in images. Review regeneration
before using it; rasterization can differ between renderer versions.

## Loader maintenance

- Standalone loader Lua omits `table` and `math`; a `flua` syntax check alone is
  not proof of loader compatibility.
- Image coordinates must stay nonzero: zero requests automatic edge placement.
  Keep the one-pixel menu viewport margin.
- Keep the splash at 480×280 RGBA to avoid VT's clipped-row-stride behavior.
- Restore loader progress settings after rendering, including on errors.
- OVMF serial mirroring can force text fallback despite a working framebuffer.
