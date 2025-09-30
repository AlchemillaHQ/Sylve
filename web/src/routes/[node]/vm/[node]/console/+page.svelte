<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import { clusterStore, currentHostname } from '$lib/stores/auth';
	import type { VMDomain } from '$lib/types/vm/vm';
	import { toHex } from '$lib/utils/string';
	import {
		Xterm,
		XtermAddon,
		type FitAddon,
		type ITerminalInitOnlyOptions,
		type ITerminalOptions,
		type Terminal
	} from '@battlefieldduck/xterm-svelte';
	import Icon from '@iconify/svelte';
	import { onDestroy, tick } from 'svelte';
	import { get } from 'svelte/store';

	interface Data {
		id: number;
		port: number;
		password: string;
		domain: VMDomain;
		hash: string;
		serial: boolean;
	}

	let { data }: { data: Data } = $props();

	const wssAuth = $state({
		hash: data.hash,
		hostname: get(currentHostname) || '',
		token: $clusterStore || ''
	});

	let consoleType = $derived.by(() => {
		if (data.serial) {
			const preferred = localStorage.getItem(`vm-${data.id}-console-preferred`);
			if (preferred === 'serial' || preferred === 'vnc') {
				return preferred;
			} else {
				return 'vnc';
			}
		} else {
			return 'vnc';
		}
	});

	let vncPath = $derived(
		`/api/vnc/${encodeURIComponent(String(data.port))}?auth=${toHex(JSON.stringify(wssAuth))}`
	);

	let vncLoading = $state(true);
	function startVncLoading() {
		vncLoading = true;
		setTimeout(() => (vncLoading = false), 1500);
	}

	$effect(() => {
		if (consoleType === 'vnc') {
			localStorage.setItem(`vm-${data.id}-console-preferred`, 'vnc');
			startVncLoading();
			serialConnected = false;
		} else if (consoleType === 'serial') {
			localStorage.setItem(`vm-${data.id}-console-preferred`, 'serial');
		}
	});

	let terminal = $state<Terminal>();
	let fitAddon: FitAddon | null = null;
	let ws: WebSocket | null = null;
	let serialLoading = $state(false);

	let serialEl: HTMLDivElement | null = null;
	let ro: ResizeObserver | null = null;

	const termOptions: ITerminalOptions & ITerminalInitOnlyOptions = {
		cursorBlink: true
	};

	function isOpen(w: WebSocket | null): boolean {
		return !!w && w.readyState === WebSocket.OPEN;
	}

	let serialConnected = $state(false);

	function sendKill(sessionId?: string) {
		if (!isOpen(ws)) return;
		serialConnected = false; // reflect intent immediately
		const body = JSON.stringify({ kill: sessionId ?? '' });
		const payload = new TextEncoder().encode('\x02' + body);
		try {
			ws!.send(payload);
		} catch {}
	}

	async function fitSoon() {
		await tick();
		await new Promise(requestAnimationFrame);
		await new Promise(requestAnimationFrame);
		fitAddon?.fit();
		if (ws && isOpen(ws)) {
			const dims = fitAddon?.proposeDimensions();
			ws.send(
				new TextEncoder().encode('\x01' + JSON.stringify({ rows: dims?.rows, cols: dims?.cols }))
			);
		}
	}

	async function serialConnect() {
		serialLoading = true;

		const headerProto = toHex(
			JSON.stringify({
				hostname: get(currentHostname) || '',
				token: $clusterStore || ''
			})
		);

		const url = `/api/vm/console?vmid=${data.id}&hash=${data.hash}`;

		if (ws) {
			try {
				ws.close();
			} catch {}
			ws = null;
		}

		ws = new WebSocket(url, [headerProto]);
		ws.binaryType = 'arraybuffer';

		ws.onopen = async () => {
			serialConnected = true;

			const Fit = new (await XtermAddon.FitAddon()).FitAddon();
			fitAddon = Fit;
			terminal?.loadAddon(Fit);

			fitAddon?.fit();
			const dims = fitAddon?.proposeDimensions();
			ws?.send(
				new TextEncoder().encode('\x01' + JSON.stringify({ rows: dims?.rows, cols: dims?.cols }))
			);

			serialLoading = false;

			fitSoon();

			if (ro) {
				ro.disconnect();
				ro = null;
			}
			if (serialEl) {
				ro = new ResizeObserver(() => {
					fitAddon?.fit();
					if (ws) {
						const d = fitAddon?.proposeDimensions();
						ws.send(
							new TextEncoder().encode('\x01' + JSON.stringify({ rows: d?.rows, cols: d?.cols }))
						);
					}
				});
				ro.observe(serialEl);
			}
		};

		ws.onmessage = (e) => {
			if (e.data instanceof ArrayBuffer) {
				terminal?.write(new Uint8Array(e.data));
			}
		};

		ws.onclose = () => {
			serialLoading = false;
			serialConnected = false;
		};

		ws.onerror = () => {
			serialLoading = false;
			serialConnected = false;
		};
	}

	function onTermLoad() {
		serialConnect().then(fitSoon);
	}

	function onTermData(data: string) {
		ws?.send(new TextEncoder().encode('\x00' + data));
	}

	onDestroy(() => {
		try {
			ws?.close();
		} catch {}
		ws = null;
		if (ro) {
			ro.disconnect();
			ro = null;
		}
		fitAddon = null;
	});
</script>

<div class="flex h-full min-h-0 w-full flex-col">
	<!-- Header -->
	{#if data.serial}
		<div class="flex h-10 w-full items-center gap-2 border-b p-2">
			<Button
				onclick={() => {
					consoleType = consoleType === 'vnc' ? 'serial' : 'vnc';
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center gap-2">
					<Icon
						icon={consoleType === 'vnc' ? 'mdi:console' : 'material-symbols:monitor-outline'}
						class="h-4 w-4"
					/>
					<span>Switch to {consoleType === 'vnc' ? 'Serial' : 'VNC'} Console</span>
				</div>
			</Button>

			{#if consoleType === 'serial'}
				<Button
					size="sm"
					variant="outline"
					class="h-6.5"
					disabled={serialLoading}
					onclick={() => {
						if (serialConnected) {
							sendKill();
						} else {
							serialConnect();
						}
					}}
				>
					<div class="flex items-center gap-2">
						<Icon icon={serialConnected ? 'mdi:power' : 'mdi:refresh'} class="h-4 w-4" />
						<span>{serialConnected ? 'Kill Serial Session' : 'Reconnect Serial'}</span>
					</div>
				</Button>
			{/if}
		</div>
	{/if}

	{#if data.domain && data.domain.status !== 'Shutoff'}
		{#if consoleType === 'vnc'}
			<div class="relative flex min-h-0 flex-1 flex-col">
				<iframe
					class="w-full flex-1 transition-opacity duration-500"
					class:opacity-0={vncLoading}
					class:opacity-100={!vncLoading}
					src={`/vnc/vnc.html?path=${vncPath}&password=${data.password}`}
					title="VM Console"
				/>
				{#if vncLoading}
					<div class="bg-background/50 absolute inset-0 z-10 flex items-center justify-center">
						<Icon icon="mdi:loading" class="text-primary h-10 w-10 animate-spin" />
					</div>
				{/if}
			</div>
		{:else if consoleType === 'serial'}
			<div bind:this={serialEl} class="relative flex min-h-0 flex-1 flex-col">
				<Xterm
					bind:terminal
					options={termOptions}
					onLoad={onTermLoad}
					onData={onTermData}
					class="h-full w-full"
				/>
				{#if serialLoading}
					<div class="bg-background/50 absolute inset-0 z-10 flex items-center justify-center">
						<Icon icon="mdi:loading" class="text-primary h-10 w-10 animate-spin" />
					</div>
				{/if}
			</div>
		{/if}
	{:else}
		<div class="flex flex-1 flex-col items-center justify-center space-y-3 text-center text-base">
			<Icon icon="mdi:server-off" class="text-primary dark:text-secondary h-14 w-14" />
			<div class="max-w-md">
				The VM is currently powered off.<br />
				Start the VM to access its console.
			</div>
		</div>
	{/if}
</div>
