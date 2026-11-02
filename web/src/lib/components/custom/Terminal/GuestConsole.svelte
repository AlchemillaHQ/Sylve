<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { toHex } from '$lib/utils/string';
	import { useResizeObserver, PersistedState, useDebounce, watch } from 'runed';
	import { onMount, untrack } from 'svelte';
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
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { isMac } from '$lib/hooks/is-mac.svelte';

	type FitAddonInstance = InstanceType<Awaited<ReturnType<typeof XtermAddon.FitAddon>>['FitAddon']>;

	interface Props {
		kind: string;
		name: string;
		url: string;
		node: string;
		hash: string;
		storageKey: string;
		status: 'running' | 'stopped' | 'unknown';
		pending: boolean;
	}

	let { kind, name, url, node, hash, storageKey, status, pending }: Props = $props();

	let terminal = $state<Terminal>();
	let fitAddon: FitAddonInstance | null = null;
	let ws = $state<WebSocket | null>(null);
	let wrapper = $state<HTMLElement | null>(null);
	let connectionState = $state<'disconnected' | 'connecting' | 'connected'>('disconnected');
	let connectionError = $state('');
	let connectionToken = 0;
	let readyTerminal = $state<Terminal>();
	let retryAttempts = 0;
	let retryTimer: ReturnType<typeof setTimeout> | undefined;
	let connectionTimer: ReturnType<typeof setTimeout> | undefined;

	const keyPrefix = untrack(() => storageKey);
	const cState = new PersistedState(`${keyPrefix}-state`, false);
	const theme = new PersistedState(`${keyPrefix}-theme`, {
		background: '#282c34',
		foreground: '#FFFFFF',
		fontSize: 14
	});

	const initialTheme = untrack(() => ({
		background: theme.current.background || '#282c34',
		foreground: theme.current.foreground || '#FFFFFF',
		fontSize: theme.current.fontSize || 14
	}));
	let fontSizeBindable: number = $state(initialTheme.fontSize);
	let bgThemeBindable: string = $state(initialTheme.background);
	let fgThemeBindable: string = $state(initialTheme.foreground);
	let openSettings = $state(false);

	const options: ITerminalOptions & ITerminalInitOnlyOptions = {
		cursorBlink: true,
		cursorStyle: 'bar',
		scrollback: 10000,
		fontFamily: 'Monaco, Menlo, "Courier New", monospace',
		fontSize: initialTheme.fontSize,
		theme: {
			background: initialTheme.background,
			foreground: initialTheme.foreground
		}
	};

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

	function sendSize(cols: number, rows: number) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(new TextEncoder().encode('\x01' + JSON.stringify({ rows, cols })));
	}

	function isSocketActive() {
		return connectionState === 'connected' || connectionState === 'connecting';
	}

	function disconnect() {
		cState.current = true;
		connectionError = '';
		disconnectSocket();
	}

	function disconnectSocket() {
		clearTimeout(retryTimer);
		clearTimeout(connectionTimer);
		retryTimer = undefined;
		connectionTimer = undefined;
		connectionToken += 1;
		connectionState = 'disconnected';

		const socket = ws;
		ws = null;

		if (socket) {
			socket.onopen = null;
			socket.onmessage = null;
			socket.onerror = null;
			socket.onclose = null;
			socket.close();
		}
	}

	function disconnectForStateChange() {
		cState.current = false;
		connectionError = '';
		retryAttempts = 0;
		disconnectSocket();
		terminal?.reset();
	}

	function reconnect() {
		if (isSocketActive()) return;
		disconnectSocket();
		retryAttempts = 0;
		cState.current = false;
		connect();
	}

	useResizeObserver(
		() => wrapper,
		() => {
			fitAndSend();
		}
	);

	let destroyed = $state(false);

	const connect = () => {
		if (destroyed || cState.current || !terminal || terminal !== readyTerminal || pending) return;
		if (status !== 'running') return;
		if (isSocketActive()) return;

		connectionState = 'connecting';
		connectionError = '';

		const activeConnectionToken = ++connectionToken;
		const activeTerminal = terminal;
		const wsAuth = toHex(JSON.stringify({ hash, hostname: node }));

		const socket = new WebSocket(`${url}?auth=${encodeURIComponent(wsAuth)}`);
		socket.binaryType = 'arraybuffer';
		ws = socket;
		let opened = false;

		socket.onopen = () => {
			if (destroyed || activeConnectionToken !== connectionToken || terminal !== activeTerminal)
				return;

			opened = true;
			clearTimeout(connectionTimer);
			connectionTimer = undefined;
			connectionState = 'connected';
			connectionError = '';
			activeTerminal.reset();
			requestAnimationFrame(() => {
				requestAnimationFrame(() => fitAndSend());
			});
		};

		socket.onmessage = (e) => {
			if (destroyed || activeConnectionToken !== connectionToken || terminal !== activeTerminal)
				return;

			try {
				if (e.data instanceof ArrayBuffer) {
					activeTerminal.write(new Uint8Array(e.data));
				} else {
					const message = String(e.data || `${kind} console unavailable`);
					connectionError = message;
					activeTerminal.write(message);
				}
			} catch {
				return;
			}
		};

		const endConnection = () => {
			if (activeConnectionToken !== connectionToken) return;
			disconnectSocket();
			if (destroyed || cState.current || pending) return;
			if (retryAttempts < 3) {
				retryAttempts += 1;
				connectionError = '';
				retryTimer = setTimeout(() => {
					retryTimer = undefined;
					connect();
				}, 1000);
			} else {
				connectionError =
					connectionError ||
					(opened
						? `The ${kind.toLowerCase()} console session ended.`
						: `Unable to connect to the ${kind.toLowerCase()} console.`);
			}
		};
		socket.onclose = socket.onerror = endConnection;
		connectionTimer = setTimeout(endConnection, 10000);
	};

	function onData(data: string) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(new TextEncoder().encode('\x00' + data));
	}

	async function onLoad(t: Terminal) {
		disconnectSocket();
		readyTerminal?.dispose();
		readyTerminal = undefined;
		fitAddon = null;
		terminal = t;
		const { FitAddon } = await XtermAddon.FitAddon();
		if (destroyed || terminal !== t || status !== 'running') {
			t.dispose();
			return;
		}
		fitAddon = new FitAddon();
		t.loadAddon(fitAddon);
		t.options.fontSize = theme.current.fontSize || 14;
		t.options.theme = {
			background: theme.current.background,
			foreground: theme.current.foreground
		};

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

		retryAttempts = 0;
		readyTerminal = t;

		requestAnimationFrame(() => {
			requestAnimationFrame(() => {
				if (destroyed || terminal !== t) return;
				fitAndSend();
			});
		});
	}

	function handleBeforeUnload(event: BeforeUnloadEvent) {
		if (ws && ws.readyState === WebSocket.OPEN) {
			event.preventDefault();
			event.returnValue = '';
		}
	}

	watch(
		() => [status, pending, readyTerminal] as const,
		([currentStatus, isPending]) => {
			if (currentStatus !== 'running' || isPending) {
				disconnectForStateChange();
				if (currentStatus !== 'running') {
					readyTerminal?.dispose();
					readyTerminal = undefined;
					fitAddon = null;
					terminal = undefined;
				}
				return;
			}

			connect();
		},
		{ lazy: true }
	);

	onMount(() => {
		window.addEventListener('beforeunload', handleBeforeUnload);

		return () => {
			window.removeEventListener('beforeunload', handleBeforeUnload);
			destroyed = true;
			disconnectSocket();

			applyFontSize.cancel?.();
			applyThemeDebounced.cancel?.();
			terminal?.dispose();
			readyTerminal = undefined;
			fitAddon = null;
			terminal = undefined;
		};
	});
</script>

{#if status === 'stopped'}
	<div
		class="dark:text-secondary text-primary/70 flex h-full w-full flex-col items-center justify-center space-y-3 text-center text-base"
	>
		<span class="icon-[mdi--server-off] h-14 w-14"></span>
		<div class="max-w-md">
			The {kind} is currently powered off.<br />
			Start the {kind} to access its console.
		</div>
	</div>
{:else if status !== 'running'}
	<div
		class="dark:text-secondary text-primary/70 flex h-full w-full flex-col items-center justify-center space-y-3 text-center text-base"
	>
		<span class="icon-[mdi--server-network-off] h-14 w-14"></span>
		<div class="max-w-md">The {kind.toLowerCase()} runtime state is currently unavailable.</div>
	</div>
{:else}
	<div class="flex h-full w-full flex-col">
		<div class="flex h-10 w-full shrink-0 items-center gap-2 border p-4">
			{#if connectionState === 'connected'}
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
					disabled={connectionState === 'connecting' || pending || !readyTerminal}
					onclick={reconnect}
				>
					<div class="flex items-center gap-2">
						<span class="icon-[mdi--refresh] h-4 w-4"></span>
						<span>
							{pending
								? 'Waiting...'
								: connectionState === 'connecting'
									? 'Connecting...'
									: 'Reconnect'}
						</span>
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

		{#if connectionError && connectionState === 'disconnected' && !cState.current && !pending}
			<div
				class="flex shrink-0 items-center justify-center gap-2 border-x border-b px-3 py-2 text-sm text-red-500"
			>
				<span class="icon-[mdi--alert-circle-outline] h-4 w-4"></span>
				<span>{connectionError}</span>
			</div>
		{/if}

		{#if cState.current}
			<div
				class="dark:text-secondary text-primary/70 flex min-h-0 w-full flex-1 flex-col items-center justify-center space-y-3 text-center"
			>
				<span class="icon-[mdi--lan-disconnect] h-14 w-14"></span>

				<div class="max-w-md">
					The console has been disconnected.<br />
					Click the "Reconnect" button to re-establish the connection.
				</div>
			</div>
		{/if}

		<div
			bind:this={wrapper}
			class="terminal-wrapper min-h-0 w-full flex-1 overflow-hidden"
			class:hidden={cState.current}
			style:background-color={theme.current.background}
		>
			<Xterm
				class="h-full w-full caret-transparent focus:outline-none"
				style="outline: none;"
				role="application"
				aria-label="{kind} terminal"
				tabindex={-1}
				{options}
				bind:terminal
				{onLoad}
				{onData}
				onpointerdown={() => terminal?.focus()}
			/>
		</div>
	</div>
{/if}

<Dialog.Root bind:open={openSettings}>
	<Dialog.Content class="min-w-45">
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<SpanWithIcon
					icon="icon-[tdesign--ai-terminal]"
					size="w-6 h-6"
					gap="gap-2"
					title={`Console settings - ${name}`}
				/>
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

		<div class="color-pickers grid grid-cols-2">
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
