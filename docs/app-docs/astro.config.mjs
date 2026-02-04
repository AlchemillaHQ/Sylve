// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import tailwindcss from '@tailwindcss/vite';

import react from '@astrojs/react';

const site = "http://localhost:4322/";

// https://astro.build/config
export default defineConfig({
    site,
    integrations: [starlight({
        title: 'Sylve',
        defaultLocale: 'root',
        locales: {
            root: {
                label: 'English',
                lang: 'en',
            },
            ml: {
                label: 'മലയാളം',
                lang: 'ml',
            },
        },
        logo: {
            light: './src/assets/logo-black.svg',
            dark: './src/assets/logo-white.svg',
        },
        favicon: './src/assets/logo-white.svg',

        social: [
            {
                icon: 'github',
                label: 'GitHub',
                href: 'https://github.com/AlchemillaHQ/Sylve',

            },
        ],
        components: {
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

        customCss: ['./src/styles/global.css', './src/assets/landing.css'],
    }), react()],
    vite: {
        plugins: [tailwindcss(),],
    },
});