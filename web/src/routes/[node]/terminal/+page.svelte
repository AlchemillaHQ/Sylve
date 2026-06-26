<script lang="ts">
	import { storage } from '$lib';
	import { sha256, toHex } from '$lib/utils/string';
	import { useResizeObserver, PersistedState, useDebounce } from 'runed';
	import { onMount } from 'svelte';
	import { Xterm, XtermAddon } from '@battlefieldduck/xterm-svelte';
	import type {
		ITerminalOptions,
		ITerminalInitOnlyOptions,
		Terminal
	} from '@battlefieldduck/xterm-svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import ColorPicker from 'svelte-awesome-color-picker';
	import { swatches } from '$lib/utils/terminal';
	import { page } from '$app/state';
	import { isMac } from '$lib/hooks/is-mac.svelte';

	type FitAddonInstance = InstanceType<Awaited<ReturnType<typeof XtermAddon.FitAddon>>['FitAddon']>;

	let terminal = $state<Terminal>();
	let fitAddon: FitAddonInstance | null = null;
	let ws = $state<WebSocket | null>(null);
	let wrapper = $state<HTMLElement | null>(null);
	let connectionToken = 0;
	let destroyed = false;

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

	const options: ITerminalOptions & ITerminalInitOnlyOptions = {
		cursorBlink: true,
		cursorStyle: 'bar',
		scrollback: 10000,
		fontFamily: 'Monaco, Menlo, "Courier New", monospace',
		fontSize: theme.current.fontSize || 14,
		theme: {
			background: theme.current.background,
			foreground: theme.current.foreground
		}
	};

	function sendSize(cols: number, rows: number) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(new TextEncoder().encode('\x01' + JSON.stringify({ rows, cols })));
	}

	function fitAndSend() {
		if (!terminal || !fitAddon) return;
		try {
			fitAddon.fit();
		} catch {
			return;
		}
		sendSize(terminal.cols, terminal.rows);
	}

	function setFontSize(size: number) {
		if (!terminal) return;
		const clamped = Math.max(8, Math.min(24, Math.round(size)));
		fontSizeBindable = clamped;
		theme.current.fontSize = clamped;
		terminal.options.fontSize = clamped;
		fitAndSend();
	}

	function changeFontSize(delta: number) {
		setFontSize((theme.current.fontSize || 14) + delta);
	}

	const applyFontSize = useDebounce(() => {
		setFontSize(fontSizeBindable);
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
	}, 300);

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
	}

	function reconnect() {
		cState.current = false;
		requestAnimationFrame(() => {
			requestAnimationFrame(() => {
				fitAndSend();
				connect();
			});
		});
	}

	useResizeObserver(
		() => wrapper,
		() => {
			fitAndSend();
		}
	);

	const connect = async () => {
		if (destroyed || !terminal) return;

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
		const socket = new WebSocket(`/api/info/terminal?auth=${encodeURIComponent(wsAuth)}`);
		socket.binaryType = 'arraybuffer';
		ws = socket;

		socket.onopen = () => {
			if (destroyed || activeConnectionToken !== connectionToken || terminal !== activeTerminal)
				return;

			console.log(`Host console connected`);
			requestAnimationFrame(() => {
				requestAnimationFrame(() => fitAndSend());
			});
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
	};

	async function onLoad(t: Terminal) {
		terminal = t;
		fitAddon = new (await XtermAddon.FitAddon()).FitAddon();
		t.loadAddon(fitAddon);

		t.attachCustomKeyEventHandler((e) => {
			const zoomModifier = isMac ? e.metaKey : e.ctrlKey;
			const otherModifier = isMac ? e.ctrlKey : e.metaKey;
			if (e.type === 'keydown' && zoomModifier && !e.altKey && !otherModifier) {
				if (e.key === '+' || e.key === '=') {
					e.preventDefault();
					changeFontSize(1);
					return false;
				}
				if (e.key === '-' || e.key === '_') {
					e.preventDefault();
					changeFontSize(-1);
					return false;
				}
			}
			return true;
		});

		if (destroyed) return;

		requestAnimationFrame(() => {
			requestAnimationFrame(() => {
				fitAndSend();
				if (!cState.current) connect();
			});
		});
	}

	function onData(data: string) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(new TextEncoder().encode('\x00' + data));
	}

	function handleBeforeUnload(event: BeforeUnloadEvent) {
		if (ws && ws.readyState === WebSocket.OPEN) {
			event.preventDefault();
			event.returnValue = '';
		}
	}

	onMount(() => {
		window.addEventListener('beforeunload', handleBeforeUnload);

		return () => {
			window.removeEventListener('beforeunload', handleBeforeUnload);
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

			terminal?.clear();
			terminal?.dispose();
			terminal = undefined;
		};
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="bg-background flex h-10 w-full items-center gap-2 border p-4">
		{#if ws?.readyState === WebSocket.OPEN}
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

		<div class="ml-auto">
			<Button
				variant="outline"
				size="sm"
				class="ml-auto h-6"
				onclick={() => {
					terminal?.clear();
					terminal?.focus();
				}}
			>
				<span class="icon-[mingcute--broom-line] h-4 w-4"></span>
			</Button>

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
		bind:this={wrapper}
		class="terminal-wrapper w-full min-h-0 flex-1 overflow-hidden"
		class:hidden={cState.current}
		style="background-color: {theme.current.background};"
	>
		<Xterm
			class="h-full w-full caret-transparent focus:outline-none"
			style="outline: none;"
			role="application"
			aria-label="Host terminal"
			tabindex={-1}
			{options}
			bind:terminal
			{onLoad}
			{onData}
			onpointerdown={() => terminal?.focus()}
		/>
	</div>
</div>

<Dialog.Root bind:open={openSettings}>
	<Dialog.Content class="min-w-45">
		<Dialog.Header class="p-0">
			<Dialog.Title>Host Console Settings</Dialog.Title>
		</Dialog.Header>
		<div class="grid grid-cols-1 gap-4">
			<CustomValueInput
				label="Font Size"
				type="number"
				bind:value={fontSizeBindable}
				onChange={applyFontSize}
				placeholder="14"
				classes=""
			/>
			<div class="color-pickers grid grid-cols-2 gap-2">
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

<style>
	:global(.terminal-wrapper .xterm) {
		height: 100%;
		padding: 0;
	}

	:global(.terminal-wrapper .xterm-viewport) {
		background-color: transparent !important;
	}

	:global(.color-pickers .alpha) {
		display: none;
	}

	:global(.color-pickers .color) {
		box-shadow: inset 0 0 0 1px rgb(0 0 0 / 0.25);
	}

	:global(.color-pickers .color:focus-visible),
	:global(.color-pickers input:focus-visible ~ .color) {
		outline-color: var(--ring);
	}
</style>
