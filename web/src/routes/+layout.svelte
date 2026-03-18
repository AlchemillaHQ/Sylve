<script lang="ts">
	import '@fontsource/noto-sans';
	import '@fontsource/noto-sans/700.css';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import { IsDocumentVisible, IsIdle, watch } from 'runed';
	import { fade } from 'svelte/transition';
	import { goto, preloadData } from '$app/navigation';
	import {
		isClusterTokenValid,
		isTokenValid,
		login,
		loginWithPasskey,
		isInitialized
	} from '$lib/api/auth';
	import Login from '$lib/components/custom/Login.svelte';
	import Throbber from '$lib/components/custom/Throbber.svelte';
	import Shell from '$lib/components/skeleton/Shell.svelte';
	import { Toaster } from '$lib/components/ui/sonner/index.js';
	import '$lib/utils/i18n';
	import { addTabulatorFilters } from '$lib/utils/table';
	import { mode, ModeWatcher } from 'mode-watcher';
	import { onMount } from 'svelte';
	import '../locales/main.loader.svelte.js';
	import Initialize from '$lib/components/custom/Initialization/Initialize.svelte';
	import { sleep } from '$lib/utils';
	import '../app.css';
	import { storage } from '$lib';
	import { loadLocale } from 'wuchale/load-utils';
	import type { Locales } from '$lib/types/common.js';
	import { page } from '$app/state';
	import Reboot from '$lib/components/custom/Initialization/Reboot.svelte';
	import { getBasicSettings } from '$lib/api/system/settings.js';
	import { ProgressBar } from '@prgm/sveltekit-progress-bar';
	import About from '$lib/components/custom/About.svelte';
	import { startSSEEvents, stopSSEEvents } from '$lib/api/events';
	import { onDestroy } from 'svelte';
	import { handleCommandKeydown } from '$lib/system.js';
	import Index from '$lib/components/custom/Command/Index.svelte';

	let { children } = $props();
	let initialized = $state<boolean | null>(null);
	let rebooted = $state<boolean>(false);

	let loading = $state({
		throbber: false,
		login: false,
		passkey: false,
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
			void startSSEEvents();

			loading.initialization = true;
			loading.throbber = true;

			let [isInit, isRebooted] = [false, false];

			try {
				[isInit, isRebooted] = await isInitialized();
				initialized = isInit;
				rebooted = isRebooted;
			} catch (error) {
				initialized = false;
				rebooted = false;
			}

			loading.initialization = false;

			if (initialized && rebooted && page.url.pathname === '/') {
				await preloadData('/datacenter/summary');
				await goto('/datacenter/summary', { replaceState: true });
			}

			await sleep(1500);
			loading.throbber = false;

			const basicSettings = await getBasicSettings();
			storage.enabledServices = basicSettings.services;
		} else {
			stopSSEEvents();
			storage.token = '';
			initialized = null;
			rebooted = false;
		}
	});

	onDestroy(() => {
		stopSSEEvents();
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
					[initialized, rebooted] = await isInitialized();
				} catch (error) {
					console.error('Initialization check error:', error);
					initialized = false;
					rebooted = false;
				}

				await goto('/');

				loading.initialization = false;

				const basicSettings = await getBasicSettings();
				storage.enabledServices = basicSettings.services;

				let target = toLoginPath;

				if (!target) {
					target = page.url.pathname;
				}

				if (target === '/') {
					target = '/datacenter/summary';
				}

				await preloadData(target);
				await goto(target, { replaceState: true });

				await sleep(1500);
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
				await sleep(800);
			}
		}

		loading.login = false;
		await sleep(800);
		loading.throbber = false;
		return;
	}

	async function handlePasskeyLogin(remember: boolean, toLoginPath: string = '') {
		let isError = false;
		loading.passkey = true;

		try {
			if (await loginWithPasskey(remember)) {
				loading.passkey = false;
				loading.throbber = true;
				loading.initialization = true;

				try {
					[initialized, rebooted] = await isInitialized();
				} catch (error) {
					console.error('Initialization check error:', error);
					initialized = false;
					rebooted = false;
				}

				await goto('/');

				loading.initialization = false;

				const basicSettings = await getBasicSettings();
				storage.enabledServices = basicSettings.services;

				let target = toLoginPath;
				if (!target) {
					target = page.url.pathname;
				}
				if (target === '/') {
					target = '/datacenter/summary';
				}

				await preloadData(target);
				await goto(target, { replaceState: true });

				await sleep(1500);
				loading.throbber = false;
				return;
			} else {
				isError = true;
				loading.passkey = false;
			}
		} catch (error) {
			isError = true;
			loading.passkey = false;
		} finally {
			if (!isError) {
				await sleep(800);
			}
		}

		loading.passkey = false;
		await sleep(800);
		loading.throbber = false;
	}

	const visible = new IsDocumentVisible();
	const idle = new IsIdle({ timeout: 10000 });

	watch(
		() => visible.current,
		(current) => {
			storage.visible = current;
		}
	);

	watch(
		() => idle.current,
		(current) => {
			storage.idle = current;
		}
	);

	watch(
		() => storage.token,
		(token) => {
			if (token) {
				void startSSEEvents();
			} else {
				stopSSEEvents();
			}
		}
	);

	let busy = $state(false);
</script>

<svelte:head>
	<!-- @wc-ignore -->
	<title>Sylve</title>
</svelte:head>

<svelte:document onkeydown={handleCommandKeydown} />

<Toaster />
<ModeWatcher />

<Tooltip.Provider>
	{#if loading.throbber}
		<Throbber />
	{:else if storage.token && !loading.throbber && !loading.login}
		{#if initialized === null}
			<Throbber />
		{:else if initialized === false || rebooted === false}
			{#if !initialized}
				<div transition:fade|global={{ duration: 400 }}>
					<Initialize bind:initialized />
				</div>
			{:else if !rebooted}
				<div transition:fade|global={{ duration: 400 }}>
					<Reboot />
				</div>
			{/if}
		{:else}
			<div transition:fade|global={{ duration: 400 }}>
				<ProgressBar
					id="top-loader"
					class={mode.current === 'dark' ? 'text-white' : 'text-green-500'}
					zIndex={9999}
					bind:busy
				/>
				<Shell>
					<Index />
					{@render children()}
				</Shell>
			</div>
		{/if}
	{:else}
		<div transition:fade|global={{ duration: 400 }}>
			<Login
				onLogin={handleLogin}
				onPasskeyLogin={handlePasskeyLogin}
				loading={loading.login}
				loadingPasskey={loading.passkey}
			/>
		</div>
	{/if}
</Tooltip.Provider>

<About bind:open={storage.openAbout} />

<style>
	:global(#top-loader) {
		height: 1px !important;
	}
</style>
