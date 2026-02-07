<script lang="ts">
	import { storage } from '$lib';
	import { sha256 } from '$lib/utils/string';
	import adze from 'adze';
	import { useResizeObserver, PersistedState, useDebounce } from 'runed';
	import { onMount, tick } from 'svelte';
	import { init as initGhostty, Terminal as GhosttyTerminal } from 'ghostty-web';
	import Button from '$lib/components/ui/button/button.svelte';
	import { fade } from 'svelte/transition';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import ColorPicker from 'svelte-awesome-color-picker';
	import { swatches } from '$lib/utils/terminal';

	let terminal = $state<GhosttyTerminal | null>(null);
	let ws = $state<WebSocket | null>(null);
	let terminalContainer = $state<HTMLElement | null>(null);
	let lastWidth = 0;
	let lastHeight = 0;

	let cState = new PersistedState(`host-console-state`, false);
	let theme = new PersistedState(`host-console-theme`, {
		background: '#282c34',
		foreground: '#FFFFFF',
		fontSize: 14
	});

	let fontSizeBindable: number = $state(theme.current.fontSize || 14);
	let bgThemeBindable: string = $state(theme.current.background || '#282c34');
	let fgThemeBindable: string = $state(theme.current.foreground || '#FFFFFF');
	let openSettings = $state(false);

	const applyFontSize = useDebounce(() => {
		if (!terminal) return;
		theme.current.fontSize = Math.max(8, Math.min(24, fontSizeBindable));
		terminal.options.fontSize = theme.current.fontSize;
		resizeTerminal(lastWidth, lastHeight);
	}, 200);

	const applyThemeDebounced = useDebounce(() => {
		if (!terminal) return;
		theme.current.background = bgThemeBindable;
		theme.current.foreground = fgThemeBindable;
		terminal.options.theme = {
			background: theme.current.background,
			foreground: theme.current.foreground
		};
		disconnect();
		reconnect();
	}, 300);

	function sendSize(cols: number, rows: number) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(new TextEncoder().encode('\x01' + JSON.stringify({ rows, cols })));
	}

	function disconnect() {
		cState.current = true;
		ws?.close();
		terminal?.dispose?.();
		terminal = null;
		ws = null;
	}

	async function reconnect() {
		cState.current = false;
		await tick();
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

		const cols = Math.max(2, Math.floor(width / cellWidth));
		const rows = Math.max(2, Math.floor(height / cellHeight));

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

	let destroyed = false;
	const setup = async () => {
		if (cState.current || !terminalContainer) return;

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
		ws = new WebSocket(`/api/info/terminal?hash=${hash}`);
		ws.binaryType = 'arraybuffer';

		ws.onopen = () => {
			adze.info(`Host console connected`);
			if (lastWidth && lastHeight) resizeTerminal(lastWidth, lastHeight);
		};

		ws.onmessage = (e) => {
			terminal?.write(e.data instanceof ArrayBuffer ? new Uint8Array(e.data) : e.data);
		};

		terminal.onData((data: string) => {
			ws?.send(new TextEncoder().encode('\x00' + data.replace(/\n/g, '\r')));
		});
	};

	onMount(() => {
		setup();
		return () => {
			destroyed = true;
			if (terminal) {
				terminal.clear?.();
				terminal.reset?.();
			}

			ws?.close();
			terminal?.dispose?.();
		};
	});
</script>

<div class="flex h-full w-full flex-col" transition:fade|global={{ duration: 200 }}>
	<div class="flex h-10 w-full items-center gap-2 border p-4 bg-background">
		{#if ws && !cState.current}
			<Button
				size="sm"
				class="bg-muted-foreground/40 dark:bg-muted h-6 text-black hover:bg-yellow-600 dark:text-white"
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
				class="bg-muted-foreground/40 dark:bg-muted h-6 text-black hover:bg-green-600 dark:text-white"
				onclick={reconnect}
			>
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--refresh] h-4 w-4"></span>
					<span>Reconnect</span>
				</div>
			</Button>
		{/if}

		<Button variant="outline" size="sm" class="ml-auto h-6" onclick={() => (openSettings = true)}>
			<span class="icon-[mdi--cog-outline] h-4 w-4"></span>
		</Button>
	</div>

	{#if cState.current}
		<div
			class="dark:text-secondary text-primary/70 flex h-full w-full flex-col items-center justify-center space-y-3 text-center"
		>
			<span class="icon-[mdi--lan-disconnect] h-14 w-14"></span>
			<div class="max-w-md">
				The host console has been disconnected.<br />
				Click the "Reconnect" button to start a new session.
			</div>
		</div>
	{/if}

	<div
		class="terminal-wrapper h-full w-full bg-black focus:outline-none caret-transparent"
		class:hidden={cState.current}
		tabindex="-1"
		style="outline: none;"
		bind:this={terminalContainer}
	></div>
</div>

<Dialog.Root bind:open={openSettings}>
	<Dialog.Content class="min-w-[180px]">
		<Dialog.Header class="p-0">
			<Dialog.Title>Host Console Settings</Dialog.Title>
		</Dialog.Header>
		<div class="grid grid-cols-1 gap-4 py-2">
			<CustomValueInput
				label="Font Size"
				type="number"
				bind:value={fontSizeBindable}
				onChange={applyFontSize}
				placeholder="14"
				classes=""
			/>
			<div class="grid grid-cols-2 gap-2">
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
		</div>
	</Dialog.Content>
</Dialog.Root>
