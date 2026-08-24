// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import sitemap from '@astrojs/sitemap';
import tailwindcss from '@tailwindcss/vite';
import svelte from '@astrojs/svelte';
import starlightOpenAPI, { openAPISidebarGroups } from 'starlight-openapi';
import starlightLlmsTxt from 'starlight-llms-txt';
import starlightPageActions from 'starlight-page-actions';

const site = 'https://sylve.io';

// https://astro.build/config
export default defineConfig({
    output: 'static',
    site,
    redirects: {
        '/guides/deployments/simple-virtual-machine': '/guides/one-shot-guides/simple-virtual-machine',
        '/guides/deployments/technitium-dns-jail': '/guides/one-shot-guides/technitium-dns-jail',
        '/guides/deployments/jellyfin-jail': '/guides/one-shot-guides/jellyfin-jail',
        '/guides/deployments/rocky-linux-jail': '/guides/one-shot-guides/rocky-linux-jail',
    },
    integrations: [
        sitemap(),
        starlight({
            title: 'Sylve',
            defaultLocale: 'root',
            locales: {
                root: {
                    label: 'English',
                    lang: 'en',
                },
            },
            favicon: '/favicon.svg',
            logo: {
                light: './src/assets/logo-black.svg',
                dark: './src/assets/logo-white.svg',
            },
            social: [
                {
                    icon: 'github',
                    label: 'GitHub',
                    href: 'https://github.com/AlchemillaHQ/Sylve',
                },
            ],
            components: {
                Head: './src/components/starlight/Head.astro',
                PageTitle: './src/components/starlight/PageTitle.astro',
                SiteTitle: './src/components/starlight/SiteTitle.astro',
            },
            plugins: [
                starlightOpenAPI([
                    {
                        base: 'api-reference',
                        schema: '../swagger/swagger.json',
                        sidebar: {
                            label: 'API Reference',
                            collapsed: true,
                            operations: {
                                badges: true,
                                labels: 'summary',
                            },
                            tags: {
                                sort: 'alphabetical',
                            },
                        },
                    },
                ]),
                // Keep `baseUrl` unset so this plugin provides the page UI while
                // `starlight-llms-txt` remains the sole owner of `/llms.txt`.
                starlightPageActions(),
                starlightLlmsTxt({
                    description:
                        'Official documentation for Sylve, an open-source management platform for FreeBSD virtual machines, jails, storage, networking, and system services.',
                    details:
                        'Hand-written documentation pages are also available as clean Markdown by replacing the trailing slash with `.md` (for example, `https://sylve.io/docs.md`).',
                    customSets: [
                        {
                            label: 'Node administration',
                            description: 'Guides for managing a Sylve node and its workloads.',
                            paths: ['guides/node/**'],
                        },
                        {
                            label: 'Data center administration',
                            description: 'Guides for clustering, backups, replication, and shared resources.',
                            paths: ['guides/data-center/**'],
                        },
                        {
                            label: 'One-shot guides',
                            description: 'End-to-end deployment and migration walkthroughs.',
                            paths: ['guides/one-shot-guides/**'],
                        },
                    ],
                    exclude: [
                        'guides/node/**',
                        'guides/data-center/**',
                        'guides/one-shot-guides/**',
                    ],
                    // Required because a pair of guides embed a Svelte network diagram.
                    rawContent: true,
                    optionalLinks: [
                        {
                            label: 'OpenAPI specification (JSON)',
                            url: 'https://raw.githubusercontent.com/AlchemillaHQ/Sylve/refs/heads/master/docs/swagger/swagger.json',
                            description: 'Machine-readable API definition in JSON format.',
                        },
                        {
                            label: 'OpenAPI specification (YAML)',
                            url: 'https://raw.githubusercontent.com/AlchemillaHQ/Sylve/refs/heads/master/docs/swagger/swagger.yaml',
                            description: 'Machine-readable API definition in YAML format.',
                        },
                    ],
                }),
            ],
            sidebar: [
                {
                    label: 'Start Here',
                    collapsed: false,
                    items: [
                        'docs',
                        'getting-started',
                    ],
                },
                {
                    label: 'Contributing',
                    collapsed: false,
                    items: [
                        'guides/contributing/code-contributions',
                        'guides/contributing/docs-contributions',
                        'guides/contributing/translations',
                    ],
                },
                {
                    label: 'Guides',
                    collapsed: false,
                    items: [
                        'guides',
                        {
                            label: 'Node',
                            collapsed: false,
                            items: [
                                'guides/node',
                                'guides/node/notes',
                                'guides/node/terminal',
                                {
                                    label: 'Network',
                                    collapsed: true,
                                    items: [
                                        'guides/node/network/objects',
                                        'guides/node/network/interfaces',
                                        {
                                            label: 'Switches',
                                            collapsed: true,
                                            items: [
                                                {
                                                    label: 'Manual',
                                                    slug: 'guides/node/network/switches/manual',
                                                },
                                                {
                                                    label: 'Standard',
                                                    slug: 'guides/node/network/switches/standard',
                                                },
                                            ],
                                        },
                                        'guides/node/network/routes',
                                        {
                                            label: 'DHCP & DNS',
                                            collapsed: true,
                                            items: [
                                                'guides/node/network/dhcp-dns/ranges',
                                                'guides/node/network/dhcp-dns/leases',
                                                'guides/node/network/dhcp-dns/config',
                                            ],
                                        },
                                        {
                                            label: 'Firewall',
                                            collapsed: true,
                                            items: [
                                                'guides/node/network/firewall/logs',
                                                'guides/node/network/firewall/traffic',
                                                'guides/node/network/firewall/nat',
                                                'guides/node/network/firewall/advanced',
                                            ],
                                        },
                                        {
                                            label: 'mDNS',
                                            collapsed: true,
                                            items: [
                                                'guides/node/network/mdns/records',
                                                'guides/node/network/mdns/settings',
                                            ],
                                        },
                                        {
                                            label: 'WireGuard',
                                            collapsed: true,
                                            items: [
                                                'guides/node/network/wireguard/server',
                                                'guides/node/network/wireguard/clients',
                                            ],
                                        },
                                    ],
                                },
                                {
                                    label: 'Storage',
                                    collapsed: true,
                                    items: [
                                        'guides/node/storage/file-explorer',
                                        'guides/node/storage/disks',
                                        {
                                            label: 'ZFS',
                                            collapsed: true,
                                            items: [
                                                'guides/node/storage/zfs/dashboard',
                                                'guides/node/storage/zfs/pools',
                                                {
                                                    label: 'Datasets',
                                                    collapsed: true,
                                                    items: [
                                                        'guides/node/storage/zfs/datasets/filesystems',
                                                        'guides/node/storage/zfs/datasets/volumes',
                                                        'guides/node/storage/zfs/datasets/snapshots',
                                                    ],
                                                },
                                            ],
                                        },
                                        {
                                            label: 'Samba',
                                            collapsed: true,
                                            items: [
                                                'guides/node/storage/samba/shares',
                                                'guides/node/storage/samba/settings',
                                                'guides/node/storage/samba/audit-logs',
                                            ],
                                        },
                                        {
                                            label: 'iSCSI',
                                            collapsed: true,
                                            items: [
                                                'guides/node/storage/iscsi/initiators',
                                                'guides/node/storage/iscsi/targets',
                                            ],
                                        },
                                    ],
                                },
                                {
                                    label: 'Utilities',
                                    collapsed: true,
                                    items: [
                                        'guides/node/utilities/cloud-init-templates',
                                        'guides/node/utilities/downloader',
                                    ],
                                },
                                {
                                    label: 'Services',
                                    collapsed: true,
                                    items: [
                                        'guides/node/services/certificates',
                                        'guides/node/services/dynamic-dns',
                                    ],
                                },
                                {
                                    label: 'Settings',
                                    collapsed: true,
                                    items: [
                                        {
                                            label: 'Authentication',
                                            collapsed: true,
                                            items: [
                                                {
                                                    label: 'Users',
                                                    collapsed: true,
                                                    items: [
                                                        {
                                                            label: 'Local',
                                                            slug: 'guides/node/settings/authentication/users/local',
                                                        },
                                                        {
                                                            label: 'PAM',
                                                            slug: 'guides/node/settings/authentication/users/pam',
                                                        },
                                                    ],
                                                },
                                                'guides/node/settings/authentication/groups',
                                            ],
                                        },
                                        {
                                            label: 'System',
                                            collapsed: true,
                                            items: [
                                                {
                                                    label: 'Notifications',
                                                    collapsed: true,
                                                    items: [
                                                        {
                                                            label: 'Transports',
                                                            slug: 'guides/node/settings/system/notifications/transports',
                                                        },
                                                        {
                                                            label: 'Rules',
                                                            slug: 'guides/node/settings/system/notifications/rules',
                                                        },
                                                    ],
                                                },
                                                'guides/node/settings/system/services',
                                                'guides/node/settings/system/tunables',
                                                'guides/node/settings/pci-passthrough',
                                            ],
                                        },
                                    ],
                                },
                                {
                                    label: 'Virtual Machines',
                                    collapsed: true,
                                    items: [
                                        'guides/node/virtual-machines/creation',
                                        'guides/node/virtual-machines/summary',
                                        'guides/node/virtual-machines/console',
                                        'guides/node/virtual-machines/storage',
                                        'guides/node/virtual-machines/hardware',
                                        'guides/node/virtual-machines/network',
                                        'guides/node/virtual-machines/snapshots',
                                        'guides/node/virtual-machines/backups',
                                        'guides/node/virtual-machines/options',
                                        'guides/node/virtual-machines/templates',
                                        'guides/node/virtual-machines/migration',
                                    ],
                                },
                                {
                                    label: 'Jails',
                                    collapsed: true,
                                    items: [
                                        'guides/node/jails/creation',
                                        'guides/node/jails/summary',
                                        'guides/node/jails/console',
                                        'guides/node/jails/hardware',
                                        'guides/node/jails/network',
                                        'guides/node/jails/snapshots',
                                        'guides/node/jails/backups',
                                        'guides/node/jails/options',
                                        'guides/node/jails/templates',
                                        'guides/node/jails/migration',
                                    ],
                                },
                                {
                                    label: 'CLI & Console',
                                    collapsed: true,
                                    items: [
                                        'guides/node/cli-console/getting-started',
                                        'guides/node/cli-console/tasks',
                                        {
                                            label: 'Networking',
                                            collapsed: true,
                                            items: [
                                                'guides/node/cli-console/networking/objects',
                                                'guides/node/cli-console/networking/switches',
                                            ],
                                        },
                                        {
                                            label: 'Jails',
                                            collapsed: true,
                                            items: [
                                                'guides/node/cli-console/jails/creation',
                                                'guides/node/cli-console/jails/lifecycle',
                                                'guides/node/cli-console/jails/networking-shell',
                                            ],
                                        },
                                        {
                                            label: 'Virtual Machines',
                                            collapsed: true,
                                            items: [
                                                'guides/node/cli-console/virtual-machines/creation',
                                                'guides/node/cli-console/virtual-machines/lifecycle',
                                                'guides/node/cli-console/virtual-machines/storage',
                                                'guides/node/cli-console/virtual-machines/networking',
                                                {
                                                    label: 'Configuration',
                                                    collapsed: true,
                                                    items: [
                                                        'guides/node/cli-console/virtual-machines/configuration/hardware',
                                                        'guides/node/cli-console/virtual-machines/configuration/options',
                                                    ],
                                                },
                                                {
                                                    label: 'Access',
                                                    collapsed: true,
                                                    items: [
                                                        'guides/node/cli-console/virtual-machines/access/consoles',
                                                        'guides/node/cli-console/virtual-machines/access/guest-agent',
                                                    ],
                                                },
                                                'guides/node/cli-console/virtual-machines/snapshots',
                                                'guides/node/cli-console/virtual-machines/templates',
                                                'guides/node/cli-console/virtual-machines/deletion',
                                            ],
                                        },
                                    ],
                                },
                            ],
                        },
                        {
                            label: 'Data Center',
                            collapsed: false,
                            items: [
                                'guides/data-center/summary',
                                'guides/data-center/notes',
                                'guides/data-center/cluster',
                                {
                                    label: 'Backups',
                                    collapsed: true,
                                    items: [
                                        'guides/data-center/backups/targets',
                                        'guides/data-center/backups/jobs',
                                        'guides/data-center/backups/events',
                                    ],
                                },
                                {
                                    label: 'Replication',
                                    collapsed: true,
                                    items: [
                                        'guides/data-center/replication',
                                        'guides/data-center/replication/policies',
                                        'guides/data-center/replication/events',
                                    ],
                                },
                            ],
                        },
                        {
                            label: 'One-Shot Guides',
                            collapsed: false,
                            items: [
                                'guides/one-shot-guides/simple-virtual-machine',
                                'guides/one-shot-guides/import-vmware-vm',
                                'guides/one-shot-guides/simple-jail',
                                'guides/one-shot-guides/technitium-dns-jail',
                                'guides/one-shot-guides/jellyfin-jail',
                                'guides/one-shot-guides/rocky-linux-jail',
                            ],
                        },
                        {
                            label: 'Topics',
                            collapsed: true,
                            items: [
                                'guides/advanced-topics/jailing-sylve',
                            ],
                        },
                    ],
                },
                ...openAPISidebarGroups,
            ],
            customCss: ['./src/styles/global.css', './src/assets/landing.css'],
        }),
        svelte(),
    ],
    vite: {
        plugins: [tailwindcss()],
    },
});
