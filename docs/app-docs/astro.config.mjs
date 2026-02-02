// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import tailwindcss from '@tailwindcss/vite';

import react from '@astrojs/react';

// https://astro.build/config
export default defineConfig({
    integrations: [starlight({
        title: 'Sylve',
        logo: {
            light: './src/assets/logo-black.svg',
            dark: './src/assets/logo-white.svg',
        },
        favicon: './src/assets/logo-white.svg',
        social: [
            { icon: 'discord', label: 'Discord', href: 'https://astro.build/chat' },
            {
                icon: 'github',
                label: 'GitHub',
                href: 'https://github.com/AlchemillaHQ/Sylve',

            },
        ],
        components: {
            Header: './src/components/CustomHeader.astro',
        },
        sidebar: [
            {
                label: 'Guides',
                items: [
                    // Each item here is one entry in the navigation menu.
                    { label: 'Example Guide', slug: 'guides/example' },
                ],
            },
            {
                label: 'Reference',
                autogenerate: { directory: 'reference' },
            },
        ],
        customCss: ['./src/styles/global.css', './src/fonts/font-face.css'],
    }), react()],
    vite: {
        plugins: [tailwindcss(),],
    },
});