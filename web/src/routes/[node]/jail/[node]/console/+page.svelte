<script lang="ts">
	import { page } from '$app/state';
	import { getJails, getJailStates } from '$lib/api/jail/jail';
	import { clusterStore, currentHostname, store } from '$lib/stores/auth';
	import type { Jail, JailState } from '$lib/types/jail/jail';
	import { updateCache } from '$lib/utils/http';
	import { sha256, toBase64, toHex } from '$lib/utils/string';
	import {
		Xterm,
		XtermAddon,
		type FitAddon,
		type ITerminalInitOnlyOptions,
		type ITerminalOptions,
		type Terminal
	} from '@battlefieldduck/xterm-svelte';
	import Icon from '@iconify/svelte';
	import { useQueries } from '@sveltestack/svelte-query';
	import adze from 'adze';
	import { get } from 'svelte/store';

	interface Data {
		jails: Jail[];
		jailStates: JailState[];
	}

	let terminal = $state<Terminal>();
	let ws: WebSocket;
	let fitAddon: FitAddon;
	let options: ITerminalOptions & ITerminalInitOnlyOptions = {
		cursorBlink: true
	};

	let { data }: { data: Data } = $props();
	const ctId = page.url.pathname.split('/')[3];

	const results = useQueries([
		{
			queryKey: ['jail-list'],
			queryFn: async () => {
				return await getJails();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.jails,
			onSuccess: (data: Jail[]) => {
				updateCache('jail-list', data);
			}
		},
		{
			queryKey: ['jail-states'],
			queryFn: async () => {
				return await getJailStates();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.jailStates,
			onSuccess: (data: JailState[]) => {
				updateCache('jail-states', data);
			}
		}
	]);

	let jail: Jail = $derived(
		($results[0].data as Jail[]).find((jail: Jail) => jail.ctId === parseInt(ctId)) || ({} as Jail)
	);

	let jState: JailState = $derived(
		($results[1].data as JailState[]).find((state: JailState) => state.ctId === parseInt(ctId)) ||
			({} as JailState)
	);

	async function onLoad() {
		if (!jail || !jail.ctId) return;
		terminal?.clear();
		terminal?.reset();

		const fit = new (await XtermAddon.FitAddon()).FitAddon();
		terminal?.loadAddon(fit);
		fit.fit();

		const hash = await sha256($store, 1);
		const wssAuth = {
			hostname: get(currentHostname),
			token: $clusterStore
		};

		ws = new WebSocket(`/api/jail/console?ctid=${jail.ctId}&hash=${hash}`, [
			toHex(JSON.stringify(wssAuth))
		]);

		ws.binaryType = 'arraybuffer';
		ws.onopen = () => {
			adze.info(`Jail console connected for jail ${jail.ctId}`);
			const dims = fit.proposeDimensions();
			ws.send(
				new TextEncoder().encode('\x01' + JSON.stringify({ rows: dims?.rows, cols: dims?.cols }))
			);

			fitAddon = fit;
		};

		ws.onmessage = (e) => {
			if (e.data instanceof ArrayBuffer) {
				terminal?.write(new Uint8Array(e.data));
			}
		};
	}

	function onData(data: string) {
		ws?.send(new TextEncoder().encode('\x00' + data));
	}
</script>

{#if jState && jState?.state === 'INACTIVE'}
	<div
		class="text-primary dark:text-secondary flex h-full w-full flex-col items-center justify-center space-y-3 text-center text-base"
	>
		<Icon icon="mdi:server-off" class="dark:text-secondary text-primary h-14 w-14" />
		<div class="max-w-md">
			The Jail is currently powered off.<br />
			Start the Jail to access its console.
		</div>
	</div>
{:else}
	<Xterm bind:terminal {options} {onLoad} {onData} class="h-full w-full" />
{/if}
