<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import { storage } from '$lib';
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
	import { onDestroy, tick } from 'svelte';

	type ConsoleType = 'vnc' | 'serial' | 'none';

	interface Data {
		id: number;
		vnc: boolean;
		port: number;
		password: string;
		domain: VMDomain;
		hash: string;
		serial: boolean;
	}

	let { data }: { data: Data } = $props();

	const wssAuth = $state({
		hash: data.hash,
		hostname: storage.hostname || '',
		token: storage.clusterToken || ''
	});

	function resolveInitialConsole(): ConsoleType {
		const both = data.vnc && data.serial;
		const onlyVnc = data.vnc && !data.serial;
		const onlySerial = !data.vnc && data.serial;

		if (both) {
			const preferred = localStorage.getItem(`vm-${data.id}-console-preferred`);
			if ((preferred === 'vnc' && data.vnc) || (preferred === 'serial' && data.serial)) {
				return preferred as ConsoleType;
			}
			return 'vnc'; // default to VNC when both exist
		}
		if (onlyVnc) return 'vnc';
		if (onlySerial) return 'serial';
		return 'none';
	}

	let consoleType: ConsoleType = $state(resolveInitialConsole());

	let vncPath = $derived.by(() => {
		return data.vnc
			? `/api/vnc/${encodeURIComponent(String(data.port))}?auth=${toHex(JSON.stringify(wssAuth))}`
			: '';
	});

	let vncLoading = $state(false);
	function startVncLoading() {
		if (!data.vnc) return;
		vncLoading = true;
		setTimeout(() => (vncLoading = false), 1500);
	}

	$effect(() => {
		if (consoleType === 'vnc' && data.vnc) {
			localStorage.setItem(`vm-${data.id}-console-preferred`, 'vnc');
			startVncLoading();
			serialConnected = false;
		} else if (consoleType === 'serial' && data.serial) {
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
		serialConnected = false;
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
		if (!data.serial) return;

		serialLoading = true;

		const headerProto = toHex(
			JSON.stringify({
				hostname: storage.hostname || '',
				token: storage.clusterToken || ''
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
	<!-- Header: show only if at least one console is available -->
	{#if (data.vnc || data.serial) && data.domain.status !== 'Shutoff'}
		<div class="flex h-10 w-full items-center gap-2 border-b p-2">
			<!-- Switcher: show only if BOTH consoles exist -->
			{#if data.vnc && data.serial}
				<Button
					onclick={() => {
						consoleType = consoleType === 'vnc' ? 'serial' : 'vnc';
					}}
					size="sm"
					variant="outline"
					class="h-6.5"
				>
					<div class="flex items-center gap-2">
						<span
							class={`icon-[${consoleType === 'vnc' ? 'mdi--console' : 'material-symbols--monitor-outline'}] h-4 w-4`}
						></span>
						<span>Switch to {consoleType === 'vnc' ? 'Serial' : 'VNC'} Console</span>
					</div>
				</Button>
			{/if}

			<!-- Serial control: only when Serial is selected and available -->
			{#if consoleType === 'serial' && data.serial}
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
						<span class={`icon-[${serialConnected ? 'mdi--power' : 'mdi--refresh'}] h-4 w-4`}
						></span>
						<span>{serialConnected ? 'Kill Serial Session' : 'Reconnect Serial'}</span>
					</div>
				</Button>
			{/if}
		</div>
	{/if}

	{#if data.domain && data.domain.status !== 'Shutoff'}
		{#if consoleType === 'vnc' && data.vnc}
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
						<span class="icon-[mdi--loading] text-primary h-10 w-10 animate-spin"></span>
					</div>
				{/if}
			</div>
		{:else if consoleType === 'serial' && data.serial}
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
						<span class="icon-[mdi--loading] text-primary h-10 w-10 animate-spin"></span>
					</div>
				{/if}
			</div>
		{:else}
			<div class="flex flex-1 flex-col items-center justify-center space-y-3 text-center text-base">
				<span class="icon-[mdi--monitor-off] text-primary dark:text-secondary h-14 w-14"></span>
				<div class="max-w-md">No console is configured for this VM.</div>
			</div>
		{/if}
	{:else}
		<div class="flex flex-1 flex-col items-center justify-center space-y-3 text-center text-base">
			<span class="icon-[mdi--server-off] text-primary dark:text-secondary h-14 w-14"></span>
			<div class="max-w-md">
				The VM is currently powered off.<br />
				Start the VM to access its console.
			</div>
		</div>
	{/if}
</div>
