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
	import { getLocalBasicSettings } from '$lib/api/system/settings.js';
	import { ProgressBar } from '@prgm/sveltekit-progress-bar';
	import About from '$lib/components/custom/About.svelte';
	import { startSSEEvents, stopSSEEvents } from '$lib/api/events';
	import { onDestroy } from 'svelte';
	import { connection } from '$lib/stores/api.svelte';
	import { handleCommandKeydown } from '$lib/system.js';
	import Index from '$lib/components/custom/Command/Index.svelte';
	import { resolve } from '$app/paths';
	import {
		setEnabledServicesForHostname,
		syncActiveEnabledServices
	} from '$lib/utils/enabled-services';

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
			} catch {
				initialized = false;
				rebooted = false;
			}

			loading.initialization = false;

			if (initialized && rebooted && page.url.pathname === '/') {
				await preloadData('/datacenter/summary');
				await goto(resolve('/datacenter/summary'), { replaceState: true });
			}

			await sleep(1500);
			loading.throbber = false;

			const basicSettings = await getLocalBasicSettings();
			setEnabledServicesForHostname(
				storage.localHostname || storage.hostname,
				basicSettings.services
			);
			syncActiveEnabledServices(page.url.pathname);
		} else {
			stopSSEEvents();
			storage.token = '';
			storage.localHostname = '';
			storage.enabledServices = null;
			storage.enabledServicesByHostname = {};
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

				await goto(resolve('/'));

				loading.initialization = false;

				const basicSettings = await getLocalBasicSettings();
				setEnabledServicesForHostname(
					storage.localHostname || storage.hostname,
					basicSettings.services
				);
				syncActiveEnabledServices(page.url.pathname);

				let target = toLoginPath;

				if (!target) {
					target = page.url.pathname;
				}

				if (target === '/') {
					target = resolve('/datacenter/summary');
				}

				await preloadData(target);

				// eslint-disable-next-line svelte/no-navigation-without-resolve
				await goto(target, { replaceState: true });

				await sleep(1500);
				loading.throbber = false;
				return;
			} else {
				isError = true;
				loading.login = false;
			}
		} catch {
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

				await goto(resolve('/'));

				loading.initialization = false;

				const basicSettings = await getLocalBasicSettings();
				setEnabledServicesForHostname(
					storage.localHostname || storage.hostname,
					basicSettings.services
				);
				syncActiveEnabledServices(page.url.pathname);

				let target = toLoginPath;
				if (!target) {
					target = page.url.pathname;
				}
				if (target === '/') {
					target = resolve('/datacenter/summary');
				}

				await preloadData(target);

				// eslint-disable-next-line svelte/no-navigation-without-resolve
				await goto(target, { replaceState: true });

				await sleep(1500);
				loading.throbber = false;
				return;
			} else {
				isError = true;
				loading.passkey = false;
			}
		} catch {
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
			console.log(storage.visible, 'FFF');
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
			<!-- Waiting for initialization state -->
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
				{#if connection.sseConnected === false}
					<div
						transition:fade={{ duration: 300 }}
						class="fixed inset-0 z-10000 flex flex-col items-center justify-center bg-black/70 backdrop-blur-sm"
					>
						<div
							class="flex flex-col items-center gap-3 rounded-xl border bg-background/90 px-10 py-8 shadow-2xl"
						>
							<span class="icon-[mdi--connection] w-16 h-16"></span>
							<p class="text-xl font-semibold text-foreground">Connection lost</p>
							<p class="text-sm text-muted-foreground">Trying to reconnect to the server&hellip;</p>
							<div class="mt-1 flex gap-1">
								<span
									class="inline-block h-2 w-2 animate-bounce rounded-full bg-red-500 [animation-delay:0ms]"
								></span>
								<span
									class="inline-block h-2 w-2 animate-bounce rounded-full bg-red-500 [animation-delay:150ms]"
								></span>
								<span
									class="inline-block h-2 w-2 animate-bounce rounded-full bg-red-500 [animation-delay:300ms]"
								></span>
							</div>
						</div>
					</div>
				{/if}
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
		height: 0.5px !important;
	}
</style>
