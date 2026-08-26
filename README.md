# Sylve

[![Discord](https://img.shields.io/discord/1075365732143071232?label=Discord&logo=discord&color=5865F2)](https://chat.sylve.io)
[![CI](https://github.com/AlchemillaHQ/Sylve/actions/workflows/ci.yaml/badge.svg)](https://github.com/AlchemillaHQ/Sylve/actions/workflows/ci.yaml)
[![Documentation](https://img.shields.io/badge/docs-sylve.io-blue)](https://sylve.io/docs)

https://github.com/user-attachments/assets/61ed410e-58f6-405f-80da-c4a6bcb469b8

Sylve is an open-source infrastructure management platform built for FreeBSD. It brings **Bhyve virtual machines**, **FreeBSD Jails**, **ZFS storage**, networking, firewalling, backups, and clustered operations into one modern web interface. It provides a streamlined, FreeBSD-native alternative to managing each subsystem by hand.

The backend is written in **Go**, while the frontend is built with **SvelteKit**.

**Documentation:** [sylve.io/docs](https://sylve.io/docs) | **Latest release notes:** [Sylve v0.3.0](https://github.com/AlchemillaHQ/Sylve/blob/master/docs/changelogs/v0.3.0.md)

## Features

| Area | Highlights |
| --- | --- |
| **Virtualization** | Bhyve VMs, FreeBSD Jails, templates, snapshots, consoles, passthrough, and staged migration |
| **Storage** | ZFS management, backups, early-access replication, S.M.A.R.T., Samba, and iSCSI |
| **Networking** | Switches, VLANs, DHCP, routing, PF firewall and NAT, WireGuard, and mDNS |
| **Security and services** | Authentication, certificates, Let's Encrypt, Dynamic DNS, notifications, and auditing |
| **Operations** | Clustering, dashboards, telemetry, workload activity, and local CLI and console tooling |

## Requirements

- **FreeBSD 15.0 or newer** with a configured system hostname.
- A **ZFS pool** when managing virtual machines or Jails. ZFS remains recommended for other deployments.
- **libvirt 12.5.0 or newer** when managing virtual machines. Jail-only and non-virtualization installations do not require libvirt.

## Quick start

Install the package, enable the service, and start it:

```bash
pkg install sylve
service sylve enable
service sylve start
```

Alternatively, install it from the FreeBSD Ports Collection:

```bash
cd /usr/ports/sysutils/sylve && make install clean
```

Open `https://<host>:8181` in your browser. A fresh package installation uses `admin` as both the username and password; change these credentials before exposing the node to an untrusted network.

For installation details, dependencies, configuration, and first-workload guides, see [Getting Started](https://sylve.io/getting-started/).

## Sponsors

We're proud to be sponsored by:

<p align="center">
  <a href="https://freebsdfoundation.org"><picture><source media="(prefers-color-scheme: dark)" srcset="./docs/sponsors/FreeBSD-White.png"><img src="./docs/sponsors/FreeBSD-Red.png" alt="FreeBSD Foundation" width="200"/></picture></a>
  &emsp;&emsp;&emsp;
  <a href="https://alchemilla.io"><picture><source media="(prefers-color-scheme: dark)" srcset="./docs/sponsors/Alchemilla-White.png"><img src="./docs/sponsors/Alchemilla-Dark.png" alt="Alchemilla" width="150"/></picture></a>
  &emsp;&emsp;&emsp;
  <a href="https://iptechnics.com"><picture><source media="(prefers-color-scheme: dark)" srcset="./docs/sponsors/IP-Technics-White.png"><img src="./docs/sponsors/IP-Technics-Dark.png" alt="IPTechnics" width="150"/></picture></a>
</p>

- [https://freebsdfoundation.org](https://freebsdfoundation.org)
- [https://alchemilla.io](https://alchemilla.io)
- [https://iptechnics.com](https://iptechnics.com)

You can also support the project by sponsoring us on GitHub:

[https://github.com/sponsors/AlchemillaHQ](https://github.com/sponsors/AlchemillaHQ)

## Contributing

Contributions are welcome. Please read the [contributor guide](https://sylve.io/guides/contributing/code-contributions/) before submitting a pull request.

## License

This project is licensed under the **BSD 2-Clause License**.

See the [LICENSE](LICENSE) file for details.
