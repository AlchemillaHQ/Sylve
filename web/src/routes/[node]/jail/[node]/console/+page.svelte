<script lang="ts">
	import { page } from '$app/state';
	import { storage } from '$lib';
	import { getSimpleJailById } from '$lib/api/jail/jail';
	import { jailPowerSignal } from '$lib/stores/api.svelte';
	import type { Jail, JailState } from '$lib/types/jail/jail';
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
	import type { Terminal as GhosttyTerminal } from 'ghostty-web';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import ColorPicker from 'svelte-awesome-color-picker';
	import { swatches } from '$lib/utils/terminal';
	import { sleep } from '$lib/utils';

	interface Data {
		jail: Jail;
		state: JailState;
		ctId: number;
	}

	let { data }: { data: Data } = $props();

	let terminal = $state<GhosttyTerminal | null>(null);
	let ws = $state<WebSocket | null>(null);
	let terminalContainer = $state<HTMLElement | null>(null);
	let connectionState = $state<'disconnected' | 'connecting' | 'connected'>('disconnected');
	let lastWidth = 0;
	let lastHeight = 0;
	let connectionToken = 0;
	let setupToken = 0;
	let setupPromise: Promise<void> | null = null;
	let ghosttyModulePromise: Promise<typeof import('ghostty-web')> | null = null;

	function loadGhostty() {
		if (!ghosttyModulePromise) {
			ghosttyModulePromise = import('ghostty-web');
		}

		return ghosttyModulePromise;
	}

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
		setupToken += 1;
		setupPromise = null;
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

		terminal?.dispose?.();
		terminal = null;
		ws = null;
	}

	function disconnectForStateChange() {
		cState.current = false;
		disconnectSocket(false);
	}

	function reconnect() {
		if (isSocketActive()) return;
		cState.current = false;
		void setup();
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

	const setupInternal = async (activeSetupToken: number) => {
		cState.current = false;

		if (!jail.current || !jail.current.ctId) return;
		if (jail.current && jail.current.state === 'INACTIVE') return;
		if (!terminalContainer) return;
		if (isSocketActive()) return;

		const ghostty = await loadGhostty();
		await ghostty.init();
		if (destroyed || activeSetupToken !== setupToken) return;

		terminal?.dispose?.();
		terminal = null;

		terminal = new ghostty.Terminal({
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

		if (destroyed || activeSetupToken !== setupToken) {
			terminal?.dispose?.();
			terminal = null;
			return;
		}

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
		connectionState = 'connecting';

		socket.onopen = () => {
			if (destroyed || activeConnectionToken !== connectionToken || terminal !== activeTerminal)
				return;

			connectionState = 'connected';

			console.log(`Jail console connected for jail ${data.ctId}`);
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
			if (socket.readyState !== WebSocket.OPEN) return;
			socket.send(new TextEncoder().encode('\x00' + data));
		});

		socket.onclose = socket.onerror = () => {
			if (activeConnectionToken !== connectionToken) return;
			if (ws === socket) {
				ws = null;
			}
			connectionState = 'disconnected';
		};
	};

	const setup = () => {
		if (setupPromise) return setupPromise;

		const activeSetupToken = ++setupToken;
		const currentSetup = setupInternal(activeSetupToken).finally(() => {
			if (setupPromise === currentSetup) {
				setupPromise = null;
			}
		});

		setupPromise = currentSetup;
		return currentSetup;
	};

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
		if (!cState.current) {
			void setup();
		}

		return () => {
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

			if (terminal) {
				terminal.clear?.();
				terminal.reset?.();
			}

			applyFontSize.cancel?.();
			applyThemeDebounced.cancel?.();
			terminal?.dispose?.();
			terminal = null;
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
		<div class="flex h-10 shrink-0 w-full items-center gap-2 border p-4">
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
				class="dark:text-secondary text-primary/70 flex flex-1 min-h-0 w-full flex-col items-center justify-center space-y-3 text-center"
			>
				<span class="icon-[mdi--lan-disconnect] h-14 w-14"></span>

				<div class="max-w-md">
					The console has been disconnected.<br />
					Click the "Reconnect" button to re-establish the connection.
				</div>
			</div>
		{/if}

		<div
			class="terminal-wrapper flex-1 min-h-0 w-full focus:outline-none caret-transparent"
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
	<Dialog.Content class="min-w-45">
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[tdesign--ai-terminal] w-6 h-6"></span>
					<span>Console Settings - {jail.current?.name}</span>
				</div>
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
