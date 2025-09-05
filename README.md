# Sylve

<a href="https://discord.gg/bJB826JvXK"><img src="https://img.shields.io/discord/1075365732143071232" alt="Discord"></a>
<a href="https://sylve-ci.alchemilla.io"><img src="https://sylve-ci.alchemilla.io/job/Sylve%20Build/badge/icon"></a>
<a href="https://sylve-ci.alchemilla.io"><img src="https://sylve-ci.alchemilla.io/job/Sylve%20Test/badge/icon?subject=Tests"></a>

> [!WARNING]
> This project is still in development so expect breaking changes!

https://github.com/user-attachments/assets/bf727ef4-4316-4084-a61f-cb8ec978e43d

Sylve aims to be a lightweight, open-source virtualization platform for FreeBSD, leveraging [Bhyve](https://wiki.freebsd.org/bhyve) for VMs and [Jails](https://wiki.freebsd.org/Jails) for containerization, with deep [ZFS](https://docs.freebsd.org/en/books/handbook/zfs/) integration. It seeks to provide a streamlined, Proxmox-like experience tailored for FreeBSD environments. Its backend is written in Go and the frontend is written in Svelte (with Kit).

# Development Requirements

These only apply to the development version of Sylve, the production version will be a single binary.

- Go >= 1.24
- Node.js (v20.18.2)
- NPM (v10.9.2)

# Runtime Requirements

Sylve is designed to run on FreeBSD 14.3 or later, and it is recommended to use the latest version of FreeBSD for the best experience.

## Dependencies

Running Sylve is pretty easy, but `sylve` depends on some packages that you can install using `pkg` or the corresponding port to that package. Here's a list of what you'd need:

| Dependency     | Min. version | Vendored | Optional | Purpose                                          |
| -------------- | ------------ | -------- | -------- | ------------------------------------------------ |
| smartmontools  | 7.4_2        | No       | No       | Disk health monitoring                           |
| tmux           | 3.2          | No       | No       | Terminal multiplexer, used for the (web) console |
| libvirt        | 11.1.0       | No       | No       | Virtualization API, used for Bhyve               |
| bhyve-firmware | 1.0_2        | No       | No       | Collection of Firmware for bhyve                 |
| samba419       | 4.19.9_9     | No       | No       | SMB file sharing service                         |
| jansson        | 2.14.1       | No       | No       | JSON library for C                               |

We also need to enable some services in order to run Sylve, you can drop these into `/etc/rc.conf` if you don't have it already:

```sh
ntpd_enable="YES" # Optional
ntpd_sync_on_start="YES" # Optional
zfs_enable="YES"
linux_enable="YES" # Optional
libvirtd_enable="YES"
dnsmasq_enable="YES"
rpcbind_enable="YES"
nfs_server_enable="YES"
mountd_enable="YES"
samba_server_enable="YES"
```

Enabling `rctl` is required. Do this by adding the following line to `/boot/loader.conf`:

``` sh
kern.racct.enable=1
```

> [!IMPORTANT]
> Please reboot your system after adding those entries to ensure that the
> services are started correctly and the kernel modules are loaded.

# Installation

## From source

```sh
git clone https://github.com/AlchemillaHQ/Sylve.git
cd Sylve
make
```

# Usage

```sh
cd bin/
cp -rf ../config.json.example config.json # Edit the config.json file to your liking
./sylve
```

# Contributing

Please read [CONTRIBUTING.md](docs/CONTRIBUTING.md) for details on our contributing guidelines.

# License

This project is licensed under the BSD 2-Clause License - see the [LICENSE](LICENSE) file for details.
