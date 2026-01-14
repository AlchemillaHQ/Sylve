import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';
import { wuchale } from '@wuchale/vite-plugin';
import { visualizer } from 'rollup-plugin-visualizer';

export default defineConfig({
	plugins: [
		wuchale(),
		tailwindcss(),
		sveltekit(),
		visualizer({
			emitFile: true
		})
	],
	optimizeDeps: {
		esbuildOptions: {
			target: 'esnext'
		},
		exclude: ['xterm', 'Xterm.svelte', '@battlefieldduck/xterm-svelte']
	},
	build: {
		target: 'esnext'
	}
});
