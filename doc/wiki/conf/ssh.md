# SSH Configuration for Zelta

Zelta relies on SSH for communication between replication partners. This document covers SSH configuration from basic key setup through advanced features like agent forwarding, jump hosts, and connection multiplexing.

As of version 1.1, **Zelta does not need to be installed on replication partners**. You can run Zelta from a dedicated bastion host that orchestrates replication between remote systems. This architecture provides exceptional security and operational flexibility.

## Configuration Goals

This guide will help you set up:

- **Network line-of-sight** between replication partners (including VPN or jump hosts if needed)
- **Consistent DNS resolution** between all systems
- **SSH key-based authentication** with appropriate access controls
- **SSH ControlMaster** for connection multiplexing and performance
- **Agent forwarding** for a secure Zelta Bastion (optional but recommended)

## Prerequisites

Before configuring SSH, ensure you have:

1. ZFS delegation configured on source and target systems (see [ZFS Allow Configuration](https://zelta.space/docs/conf/zfs-allow/))
2. Network connectivity between systems
3. A dedicated user account for backups (we'll use `backupuser` throughout this guide)

### Quick ZFS Delegation Reference

For modern ZFS implementations (OpenZFS 2.2+, FreeBSD 14+):

**On the source system:**
```sh
zfs allow -u backupuser hold,send,bookmark,snapshot sink
```

**On the target system:**
```sh
zfs allow -u backupuser receive:append,create,mount,readonly,clone,rename,volmode tank/Backups
```

**Note:** Older ZFS implementations, including most Linux distributions as of 2025, do not support the `receive:append` delegation. For these systems, you may need to grant broader `receive` permissions or use root access. Consult the [ZFS Allow Configuration](https://zelta.space/docs/conf/zfs-allow/) documentation for platform-specific guidance.

## Basic SSH Key Setup

If you're new to SSH key authentication, here's a minimal configuration to get started.

### Generate an SSH Key Pair

On the system where you'll run Zelta (your bastion or management host):

```sh
ssh-keygen -t ed25519 -C "zelta-backup-key"
```

Follow the prompts. For automated replication, you may choose to use a passphrase-protected key with `ssh-agent`, or an unprotected key in a secure environment.

### Copy the Key to Remote Systems

Use `ssh-copy-id` to install your public key on each replication partner:

```sh
# Copy to source system
ssh-copy-id backupuser@source

# Copy to target system
ssh-copy-id backupuser@target
```

### Test Basic Connectivity

Verify you can connect without a password:

```sh
ssh backupuser@source
ssh backupuser@target
```

If you can authenticate successfully, you're ready to proceed.

For more detailed SSH setup guidance, see the [OpenSSH documentation](https://www.openssh.com/manual.html) or your operating system's handbook.

## The Zelta Bastion Architecture

A Zelta Bastion is a dedicated, locked-down system that orchestrates replication between remote hosts without storing SSH keys on the replication partners themselves. This architecture provides several advantages:

- **Centralized control**: All replication logic runs from a single, auditable system
- **Minimal attack surface**: Replication partners don't need SSH keys or Zelta installed
- **Flexible topology**: The bastion doesn't need to be a replication source or target
- **Enhanced security**: SSH keys never leave the bastion, and agent forwarding eliminates key storage on partners

### Understanding Replication Direction

Zelta's default behavior is **pull replication**: the target system pulls data from the source. This means:

- The target must be able to SSH to the source
- The source needs `send` permissions
- The target needs `receive` permissions

When running from a bastion where both source and target are remote, you have three options:

1. **Pull (default)**: Bastion SSHs to target, target SSHs to source
2. **Push (`--push`)**: Bastion SSHs to source, source SSHs to target
3. **Proxy (`--sync-direction=0`)**: Bastion SSHs to both, data flows through bastion

For most scenarios, pull replication with agent forwarding (described below) provides the best balance of security and performance.

### SSH Agent Forwarding: The Secret Sauce

Agent forwarding allows the bastion's SSH credentials to be used by remote systems without copying keys to those systems. This is how you achieve a fully locked-down bastion with **no SSH keys stored on replication partners**.

Here's what makes this remarkable: your source and target systems can authenticate to each other using your bastion's credentials, but they never have access to the private key itself. The bastion can be an OpenBSD system with minimal attack surface, and your replication partners only need non-destructive ZFS permissions.

This keeps private keys on the bastion instead of copying them to replication partners.

### Configuring Agent Forwarding

#### On the Bastion

Add to `~/.ssh/config`:

```
Host source target
  ForwardAgent yes
```

Or for all hosts:

```
Host *
  ForwardAgent yes
```

Start your SSH agent and add your key:

```sh
eval $(ssh-agent)
ssh-add ~/.ssh/id_ed25519
```

#### On Replication Partners

Edit `/etc/ssh/sshd_config` on both source and target systems:

```
Match User backupuser
  AllowAgentForwarding yes
```

Reload the SSH daemon:

```sh
# FreeBSD
service sshd reload

# Linux (systemd)
systemctl reload sshd

# Linux (init)
/etc/init.d/ssh reload
```

### Testing the Bastion Flow

The key test is verifying that the target can reach the source using your bastion's forwarded credentials.

**For pull replication** (target pulls from source):

From your bastion, SSH to the target and then to the source:

```sh
ssh -J target source
```

Or test the full chain:

```sh
ssh backupuser@target
# Now from the target:
ssh backupuser@source
```

If this succeeds, your agent forwarding is working correctly.

**Test with Zelta:**

```sh
zelta match backupuser@source:sink/dataset backupuser@target:tank/Backups/dataset
```

If `zelta match` can compare the dataset trees, your SSH configuration is correct and you're ready to replicate.

## Network Line-of-Sight and Jump Hosts

Replication partners need network connectivity to each other. This can be direct connectivity, a VPN, or SSH jump hosts (ProxyJump).

### SSH Jump Hosts (ProxyJump)

SSH jump hosts provide an alternative to VPNs with excellent performance characteristics. In our testing, ProxyJump outperformed all tested VPN solutions including WireGuard for ZFS replication workloads.

To configure a jump host, add to your `~/.ssh/config`:

```
Host *.internal
  ProxyJump jumphost.example.com
```

Or for a specific host:

```
Host target
  Hostname 10.2.19.77
  User backupuser
  ProxyJump jumphost.example.com
```

With this configuration, `ssh target` automatically routes through the jump host.

Jump hosts can be chained:

```
Host target
  ProxyJump bastion.example.com,internal-jump.local
```

### VPN Considerations

If you prefer VPNs, ensure:

- All replication partners can resolve each other's hostnames
- MTU is configured appropriately for your datasets
- The VPN software doesn't interfere with ZFS send/receive streams

## Performance Optimization

### Fast Encryption Protocols

SSH cipher selection can significantly impact replication performance. Modern CPUs often include hardware acceleration for AES encryption (AES-NI on x86_64).

Add to `~/.ssh/config`:

```
Host *
  Ciphers aes128-ctr,aes256-ctr
  KexAlgorithms curve25519-sha256,curve25519-sha256@libssh.org,ecdh-sha2-nistp256
  MACs umac-128-etm@openssh.com,hmac-sha2-256-etm@openssh.com
  Compression no
```

**Note on compression:** ZFS datasets are typically already compressed. SSH compression adds CPU overhead with minimal benefit for ZFS replication. We recommend leaving it disabled.

### SSH ControlMaster

ControlMaster allows SSH to reuse existing connections, dramatically reducing the overhead of Zelta's "look before you leap" approach. Since Zelta makes multiple SSH calls per operation, ControlMaster provides substantial performance improvements.

Add to `~/.ssh/config`:

```
Host *
  ControlMaster auto
  ControlPath ~/.ssh/cm-%r@%h:%p
  ControlPersist 5m
```

This configuration:

- Automatically creates multiplexed connections (`ControlMaster auto`)
- Stores connection sockets in `~/.ssh/` with a unique name per connection
- Keeps connections alive for 5 minutes after the last session closes (`ControlPersist 5m`)

Adjust `ControlPersist` based on your replication frequency. For `zelta policy` loops running every 5 minutes, a 5-10 minute persist time works well.

## Example Complete Configuration

Here's a complete `~/.ssh/config` for a Zelta Bastion:

```
# Global defaults
Host *
  ControlMaster auto
  ControlPath ~/.ssh/cm-%r@%h:%p
  ControlPersist 10m
  Ciphers aes128-ctr,aes256-ctr
  KexAlgorithms curve25519-sha256,ecdh-sha2-nistp256
  MACs umac-128-etm@openssh.com,hmac-sha2-256-etm@openssh.com
  Compression no
  ForwardAgent yes

# Internal network via jump host
Host *.internal
  ProxyJump jumphost.example.com
  User backupuser

# Specific replication partners
Host source
  Hostname source.internal
  User backupuser

Host target
  Hostname target.internal
  User backupuser
```

## Getting Help

If you encounter issues with SSH configuration:

1. **Test basic connectivity** first: `ssh -v backupuser@host` (verbose output helps diagnose issues)
2. **Verify ZFS permissions**: `zfs allow dataset` shows delegated permissions
3. **Check agent forwarding**: `ssh-add -l` should show your key on the bastion
4. **Test the full chain**: Use `zelta match` to verify end-to-end connectivity

For Zelta-specific questions, see:

- [Zelta Documentation](https://zelta.space/docs/)
- [GitHub Issues](https://github.com/bell-tower/zelta/issues)
- [Bell Tower Contact Form](https://belltower.it/contact/)

For SSH-specific questions, consult:

- `man ssh_config` - SSH client configuration
- `man sshd_config` - SSH server configuration
- [OpenSSH Documentation](https://www.openssh.com/manual.html)
