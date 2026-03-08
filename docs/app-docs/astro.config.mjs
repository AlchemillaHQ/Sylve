// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import tailwindcss from '@tailwindcss/vite';
import svelte from '@astrojs/svelte';

const site = "https://sylve.io";

// https://astro.build/config
export default defineConfig({
    output: "static",
    site,
    redirects: {
        '/docs': '/getting-started/',
        '/docs/': '/getting-started/',
    },
    integrations: [starlight({
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
        favicon: './src/assets/logo-white.svg',
        social: [
            {
                icon: 'github',
                label: 'GitHub',
                href: 'https://github.com/AlchemillaHQ/Sylve',

            },
        ],
        components: {
            FallbackContentNotice: './src/components/starlight/FallbackContentNotice.astro',
            SiteTitle: './src/components/starlight/SiteTitle.astro'
        },
        sidebar: [
            {
                label: 'Docuementation',
                autogenerate: { directory: 'getting-started' },
            },
        ],

        customCss: ['./src/styles/global.css', './src/assets/landing.css'],
    }), svelte()],
    vite: {
        plugins: [tailwindcss()],
    }
});