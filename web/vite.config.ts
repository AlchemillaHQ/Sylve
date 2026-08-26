import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig, type Plugin } from 'vite';
import { wuchale } from 'wuchale/vite';
import { visualizer } from 'rollup-plugin-visualizer';
import { fileURLToPath } from 'node:url';

const productionStub = (name: string) =>
	fileURLToPath(new URL(`./src/lib/demo/production-stubs/${name}`, import.meta.url));

const productionDemoEntries = new Map([
	['$lib/demo/fixtures', productionStub('fixtures.ts')],
	['$lib/demo/admin-fixtures', productionStub('admin-fixtures.ts')],
	['$lib/demo/host-terminal', productionStub('host-terminal.ts')],
	['$lib/demo/vm-profiles', productionStub('vm-profiles.ts')],
	['$lib/components/custom/Terminal/DemoHostRuntime.svelte', productionStub('component.svelte')],
	['$lib/components/custom/Jail/DemoJailConsole.svelte', productionStub('component.svelte')],
	['$lib/components/custom/VM/DemoVMConsole.svelte', productionStub('component.svelte')],
	['$lib/components/custom/VM/Create/DemoProfiles.svelte', productionStub('component.svelte')]
]);

function productionDemoIsolation(): Plugin {
	return {
		name: 'sylve-production-demo-isolation',
		enforce: 'pre',
		resolveId(source) {
			const cleanSource = source.split('?', 1)[0].replaceAll('\\', '/');
			for (const [request, replacement] of productionDemoEntries) {
				const sourcePath = request.replace('$lib', '/src/lib');
				if (
					cleanSource === request ||
					cleanSource.endsWith(sourcePath) ||
					cleanSource.endsWith(`${sourcePath}.ts`)
				) {
					return replacement;
				}
			}
			return null;
		}
	};
}

export default defineConfig(({ mode }) => {
	const demo = mode === 'demo';
	return {
		define: {
			__SYLVE_DEMO__: JSON.stringify(demo)
		},
		server: {
			allowedHosts: true
		},
		plugins: [
			!demo && productionDemoIsolation(),
			wuchale(),
			tailwindcss(),
			sveltekit(),
			mode === 'analyze' &&
				visualizer({
					emitFile: true
				})
		].filter(Boolean),
		optimizeDeps: {
			exclude: ['@battlefieldduck/xterm-svelte', '@alchemilla/svelte-echarts']
		},
		build: {
			target: 'esnext'
		}
	};
});
