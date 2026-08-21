// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import sitemap from '@astrojs/sitemap';
import tailwindcss from '@tailwindcss/vite';
import svelte from '@astrojs/svelte';

const site = 'https://sylve.io';

// https://astro.build/config
export default defineConfig({
    output: 'static',
    site,
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
                SiteTitle: './src/components/starlight/SiteTitle.astro',
            },
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
                            label: 'Data Center',
                            collapsed: true,
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
                            label: 'Node',
                            collapsed: true,
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
                                                'guides/node/network/switches/manual',
                                                'guides/node/network/switches/standard',
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
                                                        'guides/node/settings/authentication/users/local',
                                                        'guides/node/settings/authentication/users/pam',
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
                                                        'guides/node/settings/system/notifications/transports',
                                                        'guides/node/settings/system/notifications/rules',
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
                                        'guides/node/jails/hardware',
                                        'guides/node/jails/network',
                                        'guides/node/jails/console',
                                        'guides/node/jails/snapshots',
                                        'guides/node/jails/options',
                                    ],
                                }
                            ],
                        },
                        {
                            label: 'Deployments',
                            collapsed: false,
                            items: [
                                'guides/deployments/technitium-dns-jail',
                                'guides/deployments/jellyfin-jail',
                                'guides/deployments/rocky-linux-jail',
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
            ],
            customCss: ['./src/styles/global.css', './src/assets/landing.css'],
        }),
        svelte(),
    ],
    vite: {
        plugins: [tailwindcss()],
    },
});
