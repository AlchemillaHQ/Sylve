// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import tailwindcss from '@tailwindcss/vite';
import svelte from '@astrojs/svelte';

const site = 'https://sylve.io';

// https://astro.build/config
export default defineConfig({
    output: 'static',
    site,
    integrations: [
        starlight({
            title: 'Sylve',
            defaultLocale: 'root',
            locales: {
                root: {
                    label: 'English',
                    lang: 'en',
                },
            },
            logo: {
                light: './src/assets/logo-black.svg',
                dark: './src/assets/logo-white.svg',
            },
            favicon: '/white.svg',
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
                        {
                            label: 'Contributing',
                            collapsed: false,
                            items: [
                                'guides/contributing/translations',
                                'guides/contributing/code-contributions',
                            ],
                        },
                    ],
                },
                {
                    label: 'Guides',
                    collapsed: false,
                    items: [
                        'guides',
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
                                        {
                                            label: 'DHCP & DNS',
                                            collapsed: true,
                                            items: [
                                                'guides/node/network/dhcp-dns/ranges',
                                                'guides/node/network/dhcp-dns/leases',
                                                'guides/node/network/dhcp-dns/config',
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
                                                'guides/node/storage/zfs/pools',
                                                'guides/node/storage/zfs/datasets/filesystems',
                                                'guides/node/storage/zfs/datasets/volumes',
                                                'guides/node/storage/zfs/datasets/snapshots',
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
                                    label: 'Settings',
                                    collapsed: true,
                                    items: [
                                        'guides/node/settings/system',
                                        'guides/node/settings/pci-passthrough',
                                        {
                                            label: 'Authentication',
                                            collapsed: true,
                                            items: [
                                                'guides/node/settings/authentication/users',
                                                'guides/node/settings/authentication/groups',
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
                                        'guides/node/virtual-machines/hardware',
                                        'guides/node/virtual-machines/storage',
                                        'guides/node/virtual-machines/network',
                                        'guides/node/virtual-machines/console',
                                        'guides/node/virtual-machines/snapshots',
                                        'guides/node/virtual-machines/options',
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
                            label: 'Data Center',
                            collapsed: true,
                            items: [
                                'guides/data-center/clustering',
                                {
                                    label: 'Backups',
                                    collapsed: true,
                                    items: [
                                        'guides/data-center/backups/targets',
                                        'guides/data-center/backups/jobs',
                                    ],
                                },
                            ],
                        },
                        {
                            label: 'Advanced Topics',
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
