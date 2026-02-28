<script lang="ts">
	import { page } from '$app/state';
	import { storage } from '$lib';
	import { getJailById, getJailStateById } from '$lib/api/jail/jail';
	import type { Jail, JailState } from '$lib/types/jail/jail';
	import { updateCache } from '$lib/utils/http';
	import { sha256, toHex } from '$lib/utils/string';
	import adze from 'adze';
	import { resource, useResizeObserver, PersistedState, useDebounce } from 'runed';
	import { onMount } from 'svelte';
	import { init as initGhostty, Terminal as GhosttyTerminal } from 'ghostty-web';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import ColorPicker from 'svelte-awesome-color-picker';
	import { swatches } from '$lib/utils/terminal';

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
	let connectionToken = 0;
	let cState = new PersistedState(`jail-${data.ctId}-console-state`, false);
	let theme = new PersistedState(`jail-${data.ctId}-console-theme`, {
		background: '#282c34',
		foreground: '#FFFFFF',
		fontSize: 14
	});

	let fontSizeBindable: number = $state(theme.current.fontSize || 14);
	let bgThemeBindable: string = $state(theme.current.background || '#282c34');
	let fgThemeBindable: string = $state(theme.current.foreground || '#FFFFFF');

	const applyFontSize = useDebounce(() => {
		if (!terminal) return;
		theme.current.fontSize = Math.max(8, Math.min(24, fontSizeBindable));
		terminal.options.fontSize = theme.current.fontSize;
		resizeTerminal(lastWidth, lastHeight);
	}, 200);

	const applyThemeDebounced = useDebounce(() => {
		if (!terminal) return;

		if (
			theme.current.background === bgThemeBindable &&
			theme.current.foreground === fgThemeBindable
		) {
			return;
		}

		theme.current.background = bgThemeBindable;
		theme.current.foreground = fgThemeBindable;

		terminal.options.theme = {
			background: theme.current.background,
			foreground: theme.current.foreground
		};

		disconnect();
		reconnect();
	}, 300);

	let openSettings = $state(false);

	// svelte-ignore state_referenced_locally
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

	// svelte-ignore state_referenced_locally
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
		connectionToken += 1;

		const socket = ws;
		ws = null;

		if (socket) {
			socket.onopen = null;
			socket.onmessage = null;
			socket.onerror = null;
			socket.onclose = null;
		}

		if (socket && socket.readyState === WebSocket.OPEN) {
			const payload = JSON.stringify({ kill: '' });
			const data = new TextEncoder().encode('\x02' + payload);

			socket.send(data);
			socket.close();
		} else if (socket && socket.readyState === WebSocket.CONNECTING) {
			socket.close();
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

	function syncTerminalSizeAfterOpen() {
		requestAnimationFrame(() => {
			requestAnimationFrame(() => {
				if (!terminalContainer) return;
				const rect = terminalContainer.getBoundingClientRect();
				if (!rect.width || !rect.height) return;
				lastWidth = rect.width;
				lastHeight = rect.height;
				resizeTerminal(rect.width, rect.height);
			});
		});
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
			cursorStyle: 'bar',
			fontFamily: 'Monaco, Menlo, "Courier New", monospace',
			fontSize: theme.current.fontSize || 14,
			theme: {
				background: theme.current.background,
				foreground: theme.current.foreground
			}
		});

		terminal.open(terminalContainer);

		const hash = await sha256(storage.token || '', 1);
		const selectedHostname = page.url.pathname.split('/').filter(Boolean)[0] || '';
		if (!selectedHostname) return;
		const wsAuth = toHex(
			JSON.stringify({
				hash,
				hostname: selectedHostname,
				token: storage.clusterToken || ''
			})
		);

		const activeConnectionToken = ++connectionToken;
		const activeTerminal = terminal;
		const socket = new WebSocket(
			`/api/jail/console?ctid=${data.ctId}&auth=${encodeURIComponent(wsAuth)}`
		);
		socket.binaryType = 'arraybuffer';
		ws = socket;

		socket.onopen = () => {
			if (destroyed || activeConnectionToken !== connectionToken || terminal !== activeTerminal)
				return;

			adze.info(`Jail console connected for jail ${data.ctId}`);
			if (lastWidth && lastHeight) {
				resizeTerminal(lastWidth, lastHeight);
			} else if (terminalContainer) {
				const rect = terminalContainer.getBoundingClientRect();
				resizeTerminal(rect.width, rect.height);
			}

			syncTerminalSizeAfterOpen();
		};

		socket.onmessage = (e) => {
			if (destroyed || activeConnectionToken !== connectionToken || terminal !== activeTerminal)
				return;

			if (e.data instanceof ArrayBuffer) {
				try {
					activeTerminal?.write(new Uint8Array(e.data));
				} catch {
					return;
				}
			} else {
				try {
					activeTerminal?.write(e.data as string);
				} catch {
					return;
				}
			}
		};

		terminal.onData((data: string) => {
			const normalizedData = data.replace(/\n/g, '\r');

			if (socket.readyState !== WebSocket.OPEN) return;
			socket.send(new TextEncoder().encode('\x00' + normalizedData));
		});
	};

	onMount(() => {
		if (!cState.current) {
			setup();
		}

		return () => {
			destroyed = true;
			connectionToken += 1;

			if (ws) {
				ws.onopen = null;
				ws.onmessage = null;
				ws.onerror = null;
				ws.onclose = null;
				ws.close();
				ws = null;
			}

			if (terminal) {
				terminal.clear?.();
				terminal.reset?.();
			}

			terminal?.dispose?.();
			terminal = null;
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
	<div class="flex h-full w-full flex-col">
		<div class="flex h-10 shrink-0 w-full items-center gap-2 border p-4">
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

			<Button
				variant="outline"
				size="sm"
				class="ml-auto h-6"
				onclick={() => {
					openSettings = true;
				}}
			>
				<span class="icon-[mdi--cog-outline] h-4 w-4"></span>
			</Button>
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
			class="terminal-wrapper h-full w-full focus:outline-none caret-transparent"
			class:hidden={cState.current}
			role="application"
			aria-label="Jail terminal"
			tabindex="-1"
			style:background-color={theme.current.background}
			style="outline: none;"
			bind:this={terminalContainer}
			onpointerdown={() => terminal?.focus()}
		></div>
	</div>
{/if}

<Dialog.Root bind:open={openSettings}>
	<Dialog.Content class="min-w-[180px]">
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				Console settings - {jail.current?.name}
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid grid-cols-1">
			<CustomValueInput
				placeholder="14"
				label="Font Size"
				type="number"
				bind:value={fontSizeBindable}
				classes="flex-1 space-y-1"
				onChange={() => {
					applyFontSize();
				}}
			/>
		</div>

		<div class="grid grid-cols-2">
			<ColorPicker
				bind:hex={bgThemeBindable}
				{swatches}
				onInput={applyThemeDebounced}
				label="Background"
			/>
			<ColorPicker
				bind:hex={fgThemeBindable}
				{swatches}
				onInput={applyThemeDebounced}
				label="Foreground"
			/>
		</div>
	</Dialog.Content>
</Dialog.Root>
