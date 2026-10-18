# First boot

Remove the installation media and boot the target. If GELI is enabled, unlock it
from the console. Open `https://<installed-address>:8181` and sign in as `admin`
with the administrator password chosen during installation, not the default
password used by a fresh package installation.

## Network setup

The installed network comes from the wizard or unattended plan, not the
[temporary live-network settings](checks.md#live-network-configuration).
Sylve applies it on first startup; connectivity may be unavailable until then.
The wizard preselects SLAAC for IPv6; choose **Off** to disable it or **Static**
for manual addressing.

The installer creates a standard switch named `lan` with the selected install-time
interface as its port. Host addressing belongs to the bridge. Port selection uses
interface names, not MAC or PCI identity.

If no usable interface was selected, the switch has no ports. Bootstrap can also
drop interfaces that no longer exist, such as a virtual NIC used to prepare disks
for another machine. Use the local console to identify and attach the real NIC:

```sh
sylve switches list
sylve switches edit --type standard --id SWITCH_ID --ports INTERFACE
```

Replace `SWITCH_ID` with the switch's ID and `INTERFACE` with the actual FreeBSD
interface name. A switch without an attached port cannot provide external
connectivity. Persistent DNS is managed by Sylve, not by the installer's temporary
resolver setup for package downloads.

## Bootstrap and service setup

The installer writes `/usr/local/etc/sylve/config.json` and `bootstrap.json`.
Sylve applies services, network settings and ZFS pool registration, then archives
a successful bootstrap as `bootstrap.json.applied`. UFS installations omit pool
registration. Service prechecks may leave unsupported services disabled.

If Sylve is unavailable, use the target's console to inspect service status with
`service sylve status` and review startup errors before changing networking or
reapplying bootstrap.

For maintainers, the [installer engine](../backend/internal/engine/sylve.go)
produces these files; Sylve's [configuration](../../internal/config/) and
[bootstrap](../../internal/bootstrap/) packages own their formats and behavior.
Update producer and consumer together rather than adding a second first-boot
network or service manager.
