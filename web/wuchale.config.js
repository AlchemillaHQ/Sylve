// @ts-check
import { adapter as svelte } from '@wuchale/svelte';
import { adapter as js } from 'wuchale/adapter-vanilla';
import { defineConfig } from 'wuchale';

export default defineConfig({
    locales: ['en', 'mal', 'hi', 'zh-CN'],
    adapters: {
        main: svelte({ sourceLocale: 'en', loader: 'sveltekit' }),
        js: js({
            loader: 'vite',
            files: ['src/**/+{page,layout}.{js,ts}', 'src/**/+{page,layout}.server.{js,ts}']
        })
    }
});
