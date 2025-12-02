<script lang="ts">
	import { storage } from '$lib';
	import { getJailById, getJailStateById } from '$lib/api/jail/jail';
	import type { Jail, JailState } from '$lib/types/jail/jail';
	import { updateCache } from '$lib/utils/http';
	import { sha256, toHex } from '$lib/utils/string';
	import adze from 'adze';
	import { resource, useInterval, useResizeObserver } from 'runed';
	import { onMount } from 'svelte';
	import { init as initGhostty, Terminal as GhosttyTerminal } from 'ghostty-web';
	import Button from '$lib/components/ui/button/button.svelte';

	interface Data {
		jail: Jail;
		state: JailState;
		ctId: number;
	}

	let { data }: { data: Data } = $props();

	let terminal = $state<GhosttyTerminal | null>(null);
	let ws: WebSocket | null = null;
	let terminalContainer = $state<HTMLElement | null>(null);
	let lastWidth = 0;
	let lastHeight = 0;

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

	onMount(() => {
		let destroyed = false;

		const setup = async () => {
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

				// initial size once WS is open
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

		setup();

		return () => {
			destroyed = true;
			ws?.close();
			terminal?.dispose?.();
		};
	});
</script>

{#if jState.current && jState.current.state === 'INACTIVE'}
	<div
		class="text-primary dark:text-secondary flex h-full w-full flex-col items-center justify-center space-y-3 text-center text-base"
	>
		<span class="icon-[mdi--server-off] dark:text-secondary text-primary h-14 w-14"></span>

		<div class="max-w-md">
			The Jail is currently powered off.<br />
			Start the Jail to access its console.
		</div>
	</div>
{:else}
	<div class="flex h-full w-full flex-col">
		<div class="flex h-10 w-full items-center gap-2 border p-4"></div>
		<div
			class="terminal-wrapper h-full w-full"
			tabindex="0"
			style="outline: none;"
			bind:this={terminalContainer}
		/>
	</div>
{/if}
