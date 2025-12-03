<script lang="ts">
	import { storage } from '$lib';
	import { getJailById, getJailStateById } from '$lib/api/jail/jail';
	import type { Jail, JailState } from '$lib/types/jail/jail';
	import { updateCache } from '$lib/utils/http';
	import { sha256, toHex } from '$lib/utils/string';
	import adze from 'adze';
	import { resource, useResizeObserver, PersistedState } from 'runed';
	import { onMount } from 'svelte';
	import { init as initGhostty, Terminal as GhosttyTerminal } from 'ghostty-web';
	import Button from '$lib/components/ui/button/button.svelte';
	import { fade } from 'svelte/transition';

	interface Data {
		jail: Jail;
		state: JailState;
		ctId: number;
	}

	let { data }: { data: Data } = $props();

	let terminal = $state<GhosttyTerminal | null>(null);
	let ws = $state<WebSocket | null>(null);
	let terminalContainer = $state<HTMLElement | null>(null);
	let lastWidth = 0;
	let lastHeight = 0;
	let cState = new PersistedState(`jail-${data.ctId}-console-state`, false);

	const jail = resource(
		() => `jail-${data.jail.ctId}`,
		async () => {
			const jail = await getJailById(data.jail.ctId, 'ctid');
			updateCache(`jail-${data.jail.ctId}`, jail);
			return jail;
		},
		{
			initialValue: data.jail
		}
	);

	const jState = resource(
		() => `jail-${data.state.ctId}-state`,
		async () => {
			const state = await getJailStateById(data.state.ctId);
			updateCache(`jail-${data.state.ctId}-state`, state);
			return state;
		},
		{
			initialValue: data.state
		}
	);

	function sendSize(cols: number, rows: number) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(new TextEncoder().encode('\x01' + JSON.stringify({ rows, cols })));
	}

	function disconnect() {
		cState.current = true;

		if (ws && ws.readyState === WebSocket.OPEN) {
			const payload = JSON.stringify({ kill: '' });
			const data = new TextEncoder().encode('\x02' + payload);

			ws.send(data);
			ws.close();
		}

		terminal?.dispose?.();
		terminal = null;
		ws = null;
	}

	function reconnect() {
		cState.current = false;
		setup();
	}

	function resizeTerminal(width: number, height: number) {
		if (!terminal) return;

		const root = terminal.element as HTMLElement | undefined;
		if (!root) return;

		const canvas = root.querySelector('canvas') as HTMLCanvasElement | null;
		if (!canvas) return;

		const currentCols = terminal.cols || 80;
		const currentRows = terminal.rows || 24;

		const cellWidth = canvas.clientWidth / currentCols || 8;
		const cellHeight = canvas.clientHeight / currentRows || 16;
		if (!cellWidth || !cellHeight) return;

		const cols = Math.max(2, Math.floor(width / cellWidth));
		const rows = Math.max(2, Math.floor(height / cellHeight));
		if (!Number.isFinite(cols) || !Number.isFinite(rows)) return;

		terminal.resize(cols, rows);
		sendSize(cols, rows);
	}

	useResizeObserver(
		() => terminalContainer,
		(entries) => {
			const entry = entries[0];
			if (!entry) return;
			const { width, height } = entry.contentRect;
			lastWidth = width;
			lastHeight = height;
			resizeTerminal(width, height);
		}
	);

	let destroyed = $state(false);

	const setup = async () => {
		cState.current = false;

		if (!jail.current || !jail.current.ctId) return;
		if (jState.current && jState.current.state === 'INACTIVE') return;
		if (!terminalContainer) return;

		await initGhostty();
		if (destroyed) return;

		terminal = new GhosttyTerminal({
			cursorBlink: true,
			fontFamily: 'Monaco, Menlo, "Courier New", monospace',
			fontSize: 14,
			theme: {
				background: '#282c34',
				foreground: '#FFFFFF'
			}
		});

		terminal.open(terminalContainer);

		const hash = await sha256(storage.token || '', 1);
		const wssAuth = {
			hostname: storage.hostname,
			token: storage.clusterToken
		};

		ws = new WebSocket(`/api/jail/console?ctid=${data.ctId}&hash=${hash}`, [
			toHex(JSON.stringify(wssAuth))
		]);
		ws.binaryType = 'arraybuffer';

		ws.onopen = () => {
			adze.info(`Jail console connected for jail ${data.ctId}`);
			if (lastWidth && lastHeight) {
				resizeTerminal(lastWidth, lastHeight);
			} else if (terminalContainer) {
				const rect = terminalContainer.getBoundingClientRect();
				resizeTerminal(rect.width, rect.height);
			}
		};

		ws.onmessage = (e) => {
			if (e.data instanceof ArrayBuffer) {
				terminal?.write(new Uint8Array(e.data));
			} else {
				terminal?.write(e.data as string);
			}
		};

		terminal.onData((data: string) => {
			ws?.send(new TextEncoder().encode('\x00' + data));
		});
	};

	onMount(() => {
		if (!cState.current) {
			setup();
		}

		return () => {
			destroyed = true;
			ws?.close();
			terminal?.dispose?.();
		};
	});
</script>

{#if jState.current && jState.current.state === 'INACTIVE'}
	<div
		class="dark:text-secondary text-primary/70 flex h-full w-full flex-col items-center justify-center space-y-3 text-center text-base"
	>
		<span class="icon-[mdi--server-off] h-14 w-14"></span>
		<div class="max-w-md">
			The Jail is currently powered off.<br />
			Start the Jail to access its console.
		</div>
	</div>
{:else}
	<div class="flex h-full w-full flex-col" transition:fade|global={{ duration: 200 }}>
		<div class="flex h-10 w-full items-center gap-2 border p-4">
			{#if ws?.OPEN === 1}
				<Button
					size="sm"
					class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
					onclick={disconnect}
				>
					<div class="flex items-center gap-2">
						<span class="icon-[mdi--close-circle-outline] h-4 w-4"></span>
						<span>Disconnect</span>
					</div>
				</Button>
			{:else}
				<Button
					size="sm"
					class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-green-600 disabled:hover:bg-neutral-600 dark:text-white"
					onclick={reconnect}
				>
					<div class="flex items-center gap-2">
						<span class="icon-[mdi--refresh] h-4 w-4"></span>
						<span>Reconnect</span>
					</div>
				</Button>
			{/if}
		</div>

		{#if cState.current}
			<div
				class="dark:text-secondary text-primary/70 flex h-full w-full flex-col items-center justify-center space-y-3 text-center"
			>
				<span class="icon-[mdi--lan-disconnect] h-14 w-14"></span>

				<div class="max-w-md">
					The console has been disconnected.<br />
					Click the "Reconnect" button to re-establish the connection.
				</div>
			</div>
		{/if}

		<div
			class="terminal-wrapper h-full w-full"
			class:hidden={cState.current}
			tabindex="0"
			style="outline: none;"
			bind:this={terminalContainer}
		/>
	</div>
{/if}
