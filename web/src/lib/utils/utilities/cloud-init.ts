import type { Column, Row } from "$lib/types/components/tree-table";
import type { CloudInitTemplate } from "$lib/types/utilities/cloud-init";
import { generateNanoId } from "../string";

export const cloudInitPlaceholders = {
    data: `#cloud-config\nusers:\n  - name: <username>\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    passwd: "$6$c8XPKY..."\n    lock_passwd: false\n    ssh_authorized_keys:\n      - ssh-rsa AAAAB3NzaC1yc2EAAAADAQ...\n\nssh_pwauth: true`,
    metadata: `instance-id: iid-local01\nlocal-hostname: test`,
    networkConfig: `# Leave blank for DHCP`,
};

export function generateTableData(data: CloudInitTemplate[]): { rows: Row[]; columns: Column[] } {
    const columns: Column[] = [
        {
            field: "id",
            title: "ID",
            visible: false
        },
        {
            field: "name",
            title: "Name"
        },
        {
            field: "data.user",
            title: "User Data",
            formatter(cell, formatterParams, onRendered) {
                const data = cell.getValue();
                return data ? data.substring(0, 30) + (data.length > 30 ? "..." : "") : "";
            },
        },
        {
            field: "data.metadata",
            title: "Metadata",
            formatter(cell, formatterParams, onRendered) {
                const data = cell.getValue();
                return data ? data.substring(0, 30) + (data.length > 30 ? "..." : "") : "";
            },
        },
        {
            field: "data.networkConfig",
            title: "Network Config",
            formatter(cell, formatterParams, onRendered) {
                const data = cell.getValue();
                return data ? data.substring(0, 30) + (data.length > 30 ? "..." : "") : "";
            },
        }
    ]

    const rows = data.map((template) => ({
        id: template.id,
        name: template.name,
        data: {
            user: template.user,
            metadata: template.meta
        }
    }));

    return {
        columns: columns,
        rows: rows
    }
}

type TemplateResult = { user: string; meta: string; networkConfig: string }

const templates: Record<string, TemplateResult> = {
    simple: {
        user: `#cloud-config
hostname: demo-vm
timezone: UTC

users:
  - name: dev
    gecos: Dev User
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    ssh_import_id:
      - gh:YOUR_GITHUB_USERNAME
    lock_passwd: true

package_update: true
package_upgrade: false

packages:
  - tmux
  - nano
  - vim
  - curl
  - wget
  - git
  - htop
  - qemu-guest-agent

ssh_pwauth: false
disable_root: true

final_message: |
  Cloud-init finished.
  User: dev
  SSH keys imported from GitHub.
`,
        meta: `instance-id: sylve-vm-${generateNanoId()}
local-hostname: sylve-simple-vm
`,
        networkConfig: ``
    },
    freebsdNetworkConfig: {
        user: `#cloud-config
hostname: freebsd-network-config
timezone: UTC

users:
  - name: dev
    gecos: Dev User
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    ssh_import_id:
      - gh:YOUR_GITHUB_USERNAME
    lock_passwd: true

`,
        meta: `instance-id: sylve-vm-${generateNanoId()}
local-hostname: freebsd-network-config
`,
        networkConfig: `
ethernets:
  em0:
    addresses:
      - 192.168.0.10/24
    gateway4: 192.168.0.1
    nameservers:
      addresses:
        - 1.1.1.1
`
    },
    debianNetworkConfig: {
        user: `#cloud-config
hostname: debian-vm
timezone: UTC

users:
  - name: dev
    gecos: Dev User
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    ssh_import_id:
      - gh:YOUR_GITHUB_USERNAME
    lock_passwd: true
`,
        meta: `instance-id: debian-vm-${generateNanoId()}
local-hostname: debian-vm
`,
        networkConfig: `
version: 2
ethernets:
  enp0s3:
    dhcp4: false
    addresses:
      - 192.168.0.12/24
    gateway4: 192.168.0.1
    nameservers:
      addresses:
        - 1.1.1.1
`
    },
    docker: {
        user: `#cloud-config
hostname: docker-vm
timezone: UTC

users:
  - name: docker
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    lock_passwd: true

package_update: true

packages:
  - ca-certificates
  - curl
  - gnupg
  - lsb-release

runcmd:
  - curl -fsSL https://get.docker.com | sh
  - usermod -aG docker docker
  - systemctl enable docker
  - systemctl start docker

final_message: |
  Docker host ready.
`,
        meta: `instance-id: sylve-vm-${generateNanoId()}
local-hostname: sylve-docker-vm
`,
        networkConfig: ``
    }
}

export function generateTemplate(type: string): TemplateResult {
    return templates[type] ?? { user: '', meta: '', networkConfig: '' };
}
