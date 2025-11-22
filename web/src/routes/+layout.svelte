<script lang="ts">
	import '@fontsource/noto-sans';
	import '@fontsource/noto-sans/700.css';

	import { fade } from 'svelte/transition';
	import { goto } from '$app/navigation';
	import { isClusterTokenValid, isTokenValid, login, isInitialized } from '$lib/api/auth';
	import { browser } from '$app/environment';
	import Login from '$lib/components/custom/Login.svelte';
	import Throbber from '$lib/components/custom/Throbber.svelte';
	import Shell from '$lib/components/skeleton/Shell.svelte';
	import { Toaster } from '$lib/components/ui/sonner/index.js';
	import '$lib/utils/i18n';
	import { addTabulatorFilters } from '$lib/utils/table';
	import { QueryClient, QueryClientProvider } from '@tanstack/svelte-query';
	import { ModeWatcher } from 'mode-watcher';
	import { onMount } from 'svelte';
	import '../locales/main.loader.svelte.js';
	import Initialize from '$lib/components/custom/Initialize.svelte';
	import { sleep } from '$lib/utils';
	import '../app.css';
	import { storage } from '$lib';
	import { loadLocale } from 'wuchale/load-utils';
	import type { Locales } from '$lib/types/common.js';
	import { page } from '$app/state';

	const queryClient = new QueryClient({
		defaultOptions: {
			queries: {
				enabled: browser
			}
		}
	});

	let { children } = $props();
	let initialized = $state<boolean | null>(null);
	let loading = $state({
		throbber: false,
		login: false,
		initialization: false
	});

	onMount(async () => {
		loadLocale((storage.language || 'en') as Locales);
		addTabulatorFilters();

		const [validToken, validClusterToken] = await Promise.all([
			isTokenValid(),
			isClusterTokenValid()
		]);

		if (validToken && validClusterToken) {
			loading.initialization = true;
			loading.throbber = true;

			try {
				initialized = await isInitialized();
			} catch (error) {
				initialized = false;
			}

			loading.initialization = false;

			if (initialized && page.url.pathname === '/') {
				await goto('/datacenter/summary', { replaceState: true });
			}

			loading.throbber = false;
		} else {
			storage.token = '';
			initialized = null;
		}
	});

	async function handleLogin(
		username: string,
		password: string,
		type: string,
		remember: boolean,
		toLoginPath: string = ''
	) {
		let isError = false;
		loading.login = true;

		try {
			if (await login(username, password, type, remember)) {
				loading.login = false;
				loading.throbber = true;
				loading.initialization = true;

				try {
					initialized = await isInitialized();
				} catch (error) {
					console.error('Initialization check error:', error);
					initialized = false;
				}

				loading.initialization = false;

				// Decide where to go
				let target = toLoginPath;

				// If caller didn't pass a target, use current path
				if (!target) {
					target = page.url.pathname;
				}

				// If target is root, send to datacenter summary
				if (target === '/') {
					target = '/datacenter/summary';
				}

				await goto(target, { replaceState: true });

				// Success path: turn off throbber *after* navigation completes
				loading.throbber = false;
				return;
			} else {
				isError = true;
				loading.login = false;
			}
		} catch (error) {
			isError = true;
			loading.login = false;
		} finally {
			if (!isError) {
				await sleep(2500);
			}
		}

		loading.login = false;
		loading.throbber = false;
		return;
	}
</script>

<svelte:head>
	<!-- @wc-ignore -->
	<title>Sylve</title>
</svelte:head>

<Toaster />
<ModeWatcher />

{#if loading.throbber}
	<Throbber />
{:else if storage.hostname && storage.token && !loading.throbber}
	<QueryClientProvider client={queryClient}>
		{#if initialized === null}
			<Throbber />
		{:else if initialized === false}
			<div transition:fade|global={{ duration: 400 }}>
				<Initialize bind:initialized />
			</div>
		{:else}
			<div transition:fade|global={{ duration: 400 }}>
				<Shell>
					{@render children()}
				</Shell>
			</div>
		{/if}
	</QueryClientProvider>
{:else}
	<div transition:fade|global={{ duration: 400 }}>
		<Login onLogin={handleLogin} loading={loading.login} />
	</div>
{/if}
