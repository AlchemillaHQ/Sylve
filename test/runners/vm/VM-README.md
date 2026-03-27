# Local CI VM for ShellSpec Testing

A lightweight Ubuntu 24.04 VM for running ShellSpec tests locally, matching the GitHub Actions `ubuntu-latest` environment.

## Prerequisites

- QEMU/KVM with libvirt (`virt-install`, `virsh`)
- A working bridge network (e.g., `br0`) or the default NAT network

## Creating the VM

Download the Ubuntu Server 24.04 cloud image:

```bash
cd /var/lib/libvirt/images
sudo wget https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img
```

Create the VM:

```bash
sudo virt-install \
    --name zelta-ci \
    --ram 2048 \
    --vcpus 2 \
    --disk path=/var/lib/libvirt/images/zelta-ci.qcow2,backing_store=/var/lib/libvirt/images/noble-server-cloudimg-amd64.img,size=10 \
    --os-variant ubuntu24.04 \
    --network bridge=br0 \
    --cloud-init root-password-generate=on \
    --noautoconsole
```

Save the generated root password from the output.

## Initial Setup

Connect to the VM console to get networking up:

```bash
sudo virsh console zelta-ci    # Ctrl+] to disconnect
```

### Configure VM with setup script from GitHub
From the VM console as root, pull and run the setup script:
```bash
curl -fsSL https://raw.githubusercontent.com/bell-tower/zelta/main/test/runners/vm/vm-setup.sh | bash
```

### configure VM via scp
Find the VM's IP:

```bash
sudo virsh domifaddr zelta-ci
```

SSH in and run the setup script:

```bash
ssh root@<vm-ip>
# Copy vm-setup.sh to the VM, then:
bash vm-setup.sh
```

Copy the zelta repo to the VM:

```bash
scp -r /path/to/zelta root@<vm-ip>:/home/testuser/zelta
# Or from inside the VM:
su - testuser -c 'git clone <repo-url> ~/zelta'
```

Make sure ownership is correct:

```bash
chown -R testuser:testuser /home/testuser/zelta
```

## Running Tests

```bash
su - testuser -c ~testuser/run_test.sh
```

## VM Management

Snapshot after setup (while running):

```bash
sudo virsh snapshot-create-as zelta-ci clean-baseline --description "Fresh setup with ShellSpec and ZFS"
```

Revert to snapshot:

```bash
sudo virsh snapshot-revert zelta-ci clean-baseline
```

Start / stop / check status:

```bash
sudo virsh start zelta-ci
sudo virsh shutdown zelta-ci
sudo virsh dominfo zelta-ci
```

Delete the VM entirely:

```bash
sudo virsh destroy zelta-ci
sudo virsh undefine zelta-ci --remove-all-storage
```
