# Unattended installation

**An advertised plan can erase disks without confirmation.** Use a dedicated,
trusted provisioning network and machine-specific plan URLs. Plans are unsigned;
web authentication does not authenticate DHCP or the machine requesting a plan.
Protect the plan server and files, which contain installation passwords.

## Startup configuration

At daemon startup, the first nonempty URL wins, in this order:

1. The `SYLVE_AUTOINSTALL_URL` environment variable.
2. The `sylve.autoinstall_url` kernel environment variable.
3. DHCP option 114 or 224 in `/var/db/dhclient.leases.*`.

For example, set the kernel variable before starting the daemon:

```sh
kenv sylve.autoinstall_url=https://provisioning.example/host-a.json
```

Set `SYLVE_AUTOINSTALL=0` in the daemon's environment to disable discovery.
The daemon fetches the selected URL once; it does not try later sources if that
URL fails. Changing a URL after startup does not trigger another fetch. Do not
restart the daemon during an installation.

A failed fetch or invalid plan leaves the wizard available. Once a valid plan
starts, the kiosk follows its progress. Failures after disk preparation do not
undo writes or automatically retry the installation.

## Plan

Serve a JSON document within 1 MiB using the same format as `POST /api/install`.
Replace all disk, interface and password placeholders before advertising it.
Inspect the inventory first; device names alone are not proof that a disk is safe
to erase. [Storage objects](storage.md#api-storage-objects) describe the supported
layouts.

`adminPassword` requires at least eight characters. Set `network.mode6` explicitly
to `slaac`, `off` or `static`; the example uses the wizard's SLAAC selection.

```json
{
  "hostname": "sylve",
  "timezone": "UTC",
  "keymap": "us",
  "rootPassword": "REPLACE_WITH_ROOT_PASSWORD",
  "adminPassword": "REPLACE_WITH_ADMIN_PASSWORD",
  "network": {
    "mode": "dhcp",
    "uplink": "TARGET_INTERFACE",
    "mode6": "slaac"
  },
  "storage": {
    "mode": "zfs",
    "zfs": {
      "name": "zroot",
      "vdevs": [{ "type": "stripe", "disks": ["TARGET_DISK"] }],
      "swap": "2g",
      "encrypt": false
    }
  }
}
```

Startup discovery runs inside the daemon without an interactive login. Clients
submitting a plan themselves must first [authenticate](checks.md#remote-access),
retain the session cookie and then post to `POST /api/install`.

## DHCP configuration

FreeBSD's dhclient does not request these options by default. Configure the
server to send the chosen option even when unrequested, scoped to the provisioning
network. The URL must return the plan itself, not a directory listing.

For [dnsmasq](https://thekelleys.org.uk/dnsmasq/docs/dnsmasq-man.html), tag the
intended host and use `dhcp-option-force`. Replace the example MAC address with
the target NIC's address:

```ini
dhcp-host=02:00:00:00:00:01,set:installer-host-a
dhcp-option-force=tag:installer-host-a,224,https://provisioning.example/host-a.json
```

For [Kea](https://kea.readthedocs.io/en/kea-3.0.0/arm/dhcp4-srv.html), define option
224 as a string under `Dhcp4.option-def`. Put its URL in the target host
reservation's `option-data` and set `always-send` to `true`. Do not advertise a
destructive plan globally to unrelated clients. MAC-based selection is not an
authentication mechanism; keep the network trusted.

An existing iPXE boot chain can pass `sylve.autoinstall_url` as a kernel setting.
The installer image does not include an implemented iPXE/netboot workflow.
