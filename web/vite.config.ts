import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';
import { wuchale } from '@wuchale/vite-plugin';
import { visualizer } from 'rollup-plugin-visualizer';

export default defineConfig(({ mode }) => ({
	server: {
		allowedHosts: true
	},
	plugins: [
		wuchale(),
		tailwindcss(),
		sveltekit(),
		mode === 'analyze' &&
			visualizer({
				emitFile: true
			})
	].filter(Boolean),
	build: {
		target: 'esnext'
	}
}));