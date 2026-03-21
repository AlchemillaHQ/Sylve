import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

const config = {
    preprocess: vitePreprocess(),
    onwarn(warning, handler) {
        if (warning.code === 'state_referenced_locally') return;

        handler(warning);
    },
    kit: {
        adapter: adapter({
            fallback: 'index.html'
        }),
        prerender: { entries: [] }
    }
};

export default config;
