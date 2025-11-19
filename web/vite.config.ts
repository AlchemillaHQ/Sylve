import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';
import { wuchale } from '@wuchale/vite-plugin';

export default defineConfig({
	plugins: [wuchale(), tailwindcss(), sveltekit()],
	optimizeDeps: {
		esbuildOptions: {
			target: 'esnext'
		},
		exclude: ['xterm', 'Xterm.svelte', '@battlefieldduck/xterm-svelte']
	},
	build: {
		target: 'esnext'
	},
	server: {
		allowedHosts: ['dxb-test-1.difusedns.com', 'dxbhome.difusedns.com']
	}
});
