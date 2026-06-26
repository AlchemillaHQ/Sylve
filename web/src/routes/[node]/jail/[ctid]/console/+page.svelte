<script lang="ts">
	import { page } from '$app/state';
	import { storage } from '$lib';
	import { getSimpleJailById } from '$lib/api/jail/jail';
	import { jailPowerSignal } from '$lib/stores/api.svelte';
	import type { SimpleJail } from '$lib/types/jail/jail';
	import { updateCache } from '$lib/utils/http';
	import { sha256, toHex } from '$lib/utils/string';
	import {
		resource,
		useResizeObserver,
		PersistedState,
		useDebounce,
		useInterval,
		watch
	} from 'runed';
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
	import { sleep } from '$lib/utils';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { isMac } from '$lib/hooks/is-mac.svelte';

	type FitAddonInstance = InstanceType<Awaited<ReturnType<typeof XtermAddon.FitAddon>>['FitAddon']>;

	interface Data {
		jail: SimpleJail;
		ctId: number;
	}

	let { data }: { data: Data } = $props();

	let terminal = $state<Terminal>();
	let fitAddon: FitAddonInstance | null = null;
	let ws = $state<WebSocket | null>(null);
	let wrapper = $state<HTMLElement | null>(null);
	let connectionState = $state<'disconnected' | 'connecting' | 'connected'>('disconnected');
	let connectionToken = 0;

	// svelte-ignore state_referenced_locally
	let cState = new PersistedState(`jail-${data.ctId}-console-state`, false);

	// svelte-ignore state_referenced_locally
	let theme = new PersistedState(`jail-${data.ctId}-console-theme`, {
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

	// svelte-ignore state_referenced_locally
	const jail = resource(
		() => `simple-jail-${data.jail.ctId}`,
		async () => {
			const jail = await getSimpleJailById(data.jail.ctId, 'ctid');
			updateCache(`simple-jail-${data.jail.ctId}`, jail);
			return jail;
		},
		{
			initialValue: data.jail
		}
	);

	function sendSize(cols: number, rows: number) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(new TextEncoder().encode('\x01' + JSON.stringify({ rows, cols })));
	}

	function isSocketActive() {
		return connectionState === 'connected' || connectionState === 'connecting';
	}

	function disconnect() {
		cState.current = true;
		disconnectSocket(true);
	}

	function disconnectSocket(forceKill: boolean) {
		connectionToken += 1;
		connectionState = 'disconnected';

		const socket = ws;
		ws = null;

		if (socket) {
			socket.onopen = null;
			socket.onmessage = null;
			socket.onerror = null;
			socket.onclose = null;
		}

		if (socket && socket.readyState === WebSocket.OPEN) {
			if (forceKill) {
				const payload = JSON.stringify({ kill: '' });
				const data = new TextEncoder().encode('\x02' + payload);
				socket.send(data);
			}
			socket.close();
		} else if (socket && socket.readyState === WebSocket.CONNECTING) {
			socket.close();
		}
	}

	function disconnectForStateChange() {
		cState.current = false;
		disconnectSocket(false);
	}

	function reconnect() {
		if (isSocketActive()) return;
		cState.current = false;
		if (!terminal) return;
		void connect();
	}

	async function refetchUntilState(targetState: 'ACTIVE' | 'INACTIVE', attempts = 8) {
		for (let i = 0; i < attempts; i += 1) {
			await jail.refetch();
			if (jail.current?.state === targetState) return true;
			if (i < attempts - 1) {
				await sleep(500);
			}
		}

		return jail.current?.state === targetState;
	}

	useResizeObserver(
		() => wrapper,
		() => {
			fitAndSend();
		}
	);

	let destroyed = $state(false);

	const connect = async () => {
		if (destroyed || !terminal) return;
		if (!jail.current || !jail.current.ctId) return;
		if (jail.current.state === 'INACTIVE') return;
		if (isSocketActive()) return;

		cState.current = false;
		connectionState = 'connecting';

		const activeConnectionToken = ++connectionToken;
		const activeTerminal = terminal;

		const hash = await sha256(storage.token || '', 1);
		if (destroyed || activeConnectionToken !== connectionToken || terminal !== activeTerminal) return;

		const selectedHostname = page.url.pathname.split('/').filter(Boolean)[0] || '';
		if (!selectedHostname) {
			connectionState = 'disconnected';
			return;
		}
		const wsAuth = toHex(
			JSON.stringify({
				hash,
				hostname: selectedHostname,
				token: storage.clusterToken || ''
			})
		);

		const socket = new WebSocket(
			`/api/jail/console?ctid=${data.ctId}&auth=${encodeURIComponent(wsAuth)}`
		);
		socket.binaryType = 'arraybuffer';
		ws = socket;

		socket.onopen = () => {
			if (destroyed || activeConnectionToken !== connectionToken || terminal !== activeTerminal)
				return;

			connectionState = 'connected';

			console.log(`Jail console connected for jail ${data.ctId}`);
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

		socket.onclose = socket.onerror = () => {
			if (activeConnectionToken !== connectionToken) return;
			if (ws === socket) {
				ws = null;
			}
			connectionState = 'disconnected';
		};
	};

	function onData(data: string) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(new TextEncoder().encode('\x00' + data));
	}

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
				if (jail.current?.state !== 'INACTIVE' && !cState.current && !isSocketActive()) {
					void connect();
				}
			});
		});
	}

	function handleBeforeUnload(event: BeforeUnloadEvent) {
		if (ws && ws.readyState === WebSocket.OPEN) {
			event.preventDefault();
			event.returnValue = '';
		}
	}

	useInterval(() => 1000, {
		callback: () => {
			jail.refetch();
		}
	});

	watch(
		() => storage.idle,
		(idle) => {
			if (!idle) {
				jail.refetch();
			}
		}
	);

	watch(
		() => jail.current?.state,
		(state) => {
			if (state === 'INACTIVE') {
				disconnectForStateChange();
				return;
			}

			if (state === 'ACTIVE' && !cState.current && !isSocketActive()) {
				reconnect();
			}
		},
		{ lazy: true }
	);

	watch(
		() => jailPowerSignal.token,
		() => {
			void (async () => {
				if (jailPowerSignal.ctId !== data.ctId) return;
				if (jailPowerSignal.action === 'stop') {
					disconnectForStateChange();
					await refetchUntilState('INACTIVE');
					return;
				}

				if (jailPowerSignal.action === 'start') {
					cState.current = false;
					const isActive = await refetchUntilState('ACTIVE');
					if (isActive) {
						reconnect();
					}
				}
			})();
		},
		{ lazy: true }
	);

	onMount(() => {
		window.addEventListener('beforeunload', handleBeforeUnload);

		return () => {
			window.removeEventListener('beforeunload', handleBeforeUnload);
			destroyed = true;
			connectionToken += 1;
			connectionState = 'disconnected';

			if (ws) {
				ws.onopen = null;
				ws.onmessage = null;
				ws.onerror = null;
				ws.onclose = null;
				ws.close();
				ws = null;
			}

			applyFontSize.cancel?.();
			applyThemeDebounced.cancel?.();
			terminal?.dispose?.();
			terminal = undefined;
		};
	});
</script>

{#if jail.current && jail.current.state === 'INACTIVE'}
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
					disabled={connectionState === 'connecting'}
					onclick={reconnect}
				>
					<div class="flex items-center gap-2">
						<span class="icon-[mdi--refresh] h-4 w-4"></span>
						<span>{connectionState === 'connecting' ? 'Connecting...' : 'Reconnect'}</span>
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
				aria-label="Jail terminal"
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
					title={`Console settings - ${jail.current?.name || ''}`}
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
