import type { Column, Row } from "$lib/types/components/tree-table";
import type { CloudInitTemplate } from "$lib/types/utilities/cloud-init";
import { generateNanoId } from "../string";

export const cloudInitPlaceholders = {
    data: `#cloud-config\nusers:\n  - name: <username>\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    passwd: "$6$c8XPKY..."\n    lock_passwd: false\n    ssh_authorized_keys:\n      - ssh-rsa AAAAB3NzaC1yc2EAAAADAQ...\n\nssh_pwauth: true`,
    metadata: `instance-id: iid-local01\nlocal-hostname: test`
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

type TemplateResult = { user: string; meta: string }

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
`
    }
}

export function generateTemplate(type: string): TemplateResult {
    return templates[type] ?? { user: '', meta: '' };
}
