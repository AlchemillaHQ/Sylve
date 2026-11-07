# Getting Started with ZFS

If you're looking for enterprise storage that benefits from the simplicity and power of Zelta, you first need ZFS systems.

Note that Zelta can orchestrate backups and other workflows from anywhere, and no dependencies are required to use Zelta on the ZFS servers themselves.

---

## For Organizations

ZFS is a production filesystem that rewards careful planning. If you're deploying ZFS for business-critical data, **work with someone who has done it before**. Good guidance can save enormous costs versus the alternatives and pays dividends over time in terms of maintainability, long-term costs, and new features.

[Bell Tower](https://belltower.it/contact/) provides consulting for efficient infrastructure design.

---

## ZFS-Native Operating Systems

The easiest path to getting started with ZFS is to use an operating system where it's the default:

- **FreeBSD** — ZFS is the recommended root filesystem. Install FreeBSD and select ZFS during setup. Done.

- **Illumos distributions** (OmniOS, SmartOS) — ZFS is native and mature. Excellent for servers and storage appliances.

These systems have years of ZFS integration work and are officially supported in their entire stack.

## Linux

OpenZFS runs perfectly on Linux and is the most popular choice. Major distributions package it:

- Ubuntu includes ZFS in its installer

- Debian, Fedora, and others have packages available

The [OpenZFS documentation](https://openzfs.github.io/openzfs-docs/Getting%20Started/index.html) covers installation for various distributions.

For production deployments, we recommend using ZFS for your Linux data drives, but avoid it for your "boot" volumes.

## Appliances and Managed Solutions

If you want ZFS without managing the underlying system:

- **Cloud instances** — Many providers offer FreeBSD or Linux images where you can configure ZFS

- **ZFS hosting** — Services like [zfs.rent](https://zfs.rent) provide ready-to-use ZFS storage

- **Appliances** — There are many turnkey storage appliances with web interfaces such as TrueNAS.

---

## Next Steps

Once you have ZFS running, return to [First Backup](/docs/start/) to set up your first backup.

For production deployments, compliance requirements, or complex environments, [contact Bell Tower](https://belltower.it/contact/) for expert guidance.
