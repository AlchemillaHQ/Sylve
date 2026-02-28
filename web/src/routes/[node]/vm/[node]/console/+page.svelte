<script lang="ts">
	import { page } from '$app/state';
	import { Button } from '$lib/components/ui/button/index.js';
	import { storage } from '$lib';
	import type { VM, VMDomain } from '$lib/types/vm/vm';
	import { toHex } from '$lib/utils/string';
	import { init as initGhostty, Terminal as GhosttyTerminal } from 'ghostty-web';
	import { onDestroy, onMount, tick } from 'svelte';
	import { getVmById, getVMDomain } from '$lib/api/vm/vm';
	import { updateCache } from '$lib/utils/http';
	import {
		resource,
		useInterval,
		watch,
		PersistedState,
		useDebounce,
		useResizeObserver
	} from 'runed';
	import { mode } from 'mode-watcher';
	import adze from 'adze';
	import { fade } from 'svelte/transition';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import ColorPicker from 'svelte-awesome-color-picker';
	import { swatches } from '$lib/utils/terminal';

	type ConsoleType = 'vnc' | 'serial' | 'none';

	interface Data {
		vm: VM;
		domain: VMDomain;
		rid: string;
		hash: string;
	}

	let { data }: { data: Data } = $props();

	const vm = resource(
		() => `vm-${data.rid}`,
		async (key) => {
			const result = await getVmById(Number(data.rid), 'rid');
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.vm
		}
	);

	const domain = resource(
		() => `vm-domain-${data.rid}`,
		async (key) => {
			const result = await getVMDomain(data.rid);
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.domain
		}
	);

	function getWSSAuth() {
		const selectedHostname = page.url.pathname.split('/').filter(Boolean)[0] || '';

		return {
			hash: data.hash,
			hostname: selectedHostname,
			token: storage.clusterToken || ''
		};
	}

	function resolveInitialConsole(): ConsoleType {
		const both = vm.current.vncEnabled && vm.current.serial;
		const onlyVnc = vm.current.vncEnabled && !vm.current.serial;
		const onlySerial = !vm.current.vncEnabled && vm.current.serial;

		if (both) {
			const preferred = localStorage.getItem(`vm-${vm.current.rid}-console-preferred`);
			if (
				(preferred === 'vnc' && vm.current.vncEnabled) ||
				(preferred === 'serial' && vm.current.serial)
			) {
				return preferred as ConsoleType;
			}
			return 'vnc';
		}
		if (onlyVnc) return 'vnc';
		if (onlySerial) return 'serial';
		return 'none';
	}

	let consoleType: ConsoleType = $state(resolveInitialConsole());
	let connected = $state(false);

	let cState = new PersistedState(`vm-${data.rid}-console-state`, false);
	let theme = new PersistedState(`vm-${data.rid}-console-theme`, {
		background: '#282c34',
		foreground: '#FFFFFF',
		fontSize: 14
	});

	let fontSizeBindable: number = $state(theme.current.fontSize || 14);
	let bgThemeBindable: string = $state(theme.current.background || '#282c34');
	let fgThemeBindable: string = $state(theme.current.foreground || '#FFFFFF');
	let openSettings = $state(false);

	let terminal = $state<GhosttyTerminal | null>(null);
	let ws: WebSocket | null = null;
	let terminalContainer = $state<HTMLElement | null>(null);
	let lastWidth = 0;
	let lastHeight = 0;
	let destroyed = $state(false);

	useInterval(() => 1000, {
		callback: () => {
			domain.refetch();
		}
	});

	watch(
		() => storage.idle,
		(idle) => {
			if (!idle) {
				vm.refetch();
				domain.refetch();
			}
		}
	);

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

		if (ws?.readyState === WebSocket.OPEN) {
			cleanupSerial(false);
			serialConnect();
		}
	}, 300);

	let vncPath = $derived.by(() => {
		if (!vm.current.vncEnabled) return '';
		const wssAuth = getWSSAuth();
		return `/api/vnc/${encodeURIComponent(String(vm.current.vncPort))}?auth=${toHex(JSON.stringify(wssAuth))}`;
	});

	let vncLoading = $state(false);
	function startVncLoading() {
		if (!vm.current.vncEnabled) return;
		vncLoading = true;
		setTimeout(() => (vncLoading = false), 1500);
	}

	function sendSize(cols: number, rows: number) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(new TextEncoder().encode('\x01' + JSON.stringify({ cols, rows })));
	}

	function cleanupSerial(forceKill = false) {
		connected = false;

		if (ws) {
			ws.onclose = null;
			if (ws.readyState === WebSocket.OPEN) {
				if (forceKill) {
					const payload = JSON.stringify({ kill: '' });
					const data = new TextEncoder().encode('\x02' + payload);
					ws.send(data);
				}
				ws.close();
			}
			ws = null;
		}

		terminal?.dispose?.();
		terminal = null;
	}

	function disconnectSerial() {
		cState.current = true;
		cleanupSerial(true);
	}

	function reconnectSerial() {
		cState.current = false;
		serialConnect();
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

		terminal.focus();
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

	const serialConnect = async () => {
		cState.current = false;

		if (!vm.current.serial) return;
		if (domain.current.status === 'Shutoff') return;
		if (!terminalContainer) return;

		await initGhostty();
		if (destroyed) return;

		cleanupSerial(false);

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

		const wssAuth = getWSSAuth();
		const url = `/api/vm/console?rid=${vm.current.rid}&auth=${encodeURIComponent(toHex(JSON.stringify(wssAuth)))}`;
		ws = new WebSocket(url);
		ws.binaryType = 'arraybuffer';

		ws.onopen = async () => {
			connected = true;
			adze.info(`Serial console connected for VM ${data.rid}`);
			if (lastWidth && lastHeight) {
				resizeTerminal(lastWidth, lastHeight);
			} else if (terminalContainer) {
				const rect = terminalContainer.getBoundingClientRect();
				resizeTerminal(rect.width, rect.height);
			}

			syncTerminalSizeAfterOpen();
		};

		ws.onmessage = (e) => {
			if (e.data instanceof ArrayBuffer) {
				terminal?.write(new Uint8Array(e.data));
			} else {
				terminal?.write(e.data as string);
			}
		};

		terminal.onData((data: string) => {
			const normalizedData = data.replace(/\n/g, '\r');

			if (ws && ws.readyState === WebSocket.OPEN) {
				ws.send(new TextEncoder().encode('\x00' + normalizedData));
			}
		});

		ws.onclose = ws.onerror = () => {
			connected = false;
		};
	};

	onMount(() => {
		if (consoleType === 'vnc' && vm.current.vncEnabled) {
			startVncLoading();
		} else if (consoleType === 'serial' && vm.current.serial && !cState.current) {
			tick().then(() => {
				serialConnect();
			});
		}

		return () => {
			destroyed = true;
			if (terminal) {
				terminal.clear?.();
				terminal.reset?.();
			}

			cleanupSerial(false);
			applyFontSize.cancel?.();
			applyThemeDebounced.cancel?.();
			terminal?.dispose?.();
		};
	});

	watch(
		() => consoleType,
		(type) => {
			if (type === 'vnc' && vm.current.vncEnabled) {
				localStorage.setItem(`vm-${vm.current.rid}-console-preferred`, 'vnc');
				startVncLoading();
				cleanupSerial(false);
			} else if (type === 'serial' && vm.current.serial) {
				localStorage.setItem(`vm-${vm.current.rid}-console-preferred`, 'serial');
				if (!cState.current) {
					tick().then(() => {
						serialConnect();
					});
				}
			}
		},
		{ lazy: true }
	);
</script>

<div class="flex h-full w-full flex-col">
	{#if (vm.current.vncEnabled || vm.current.serial) && domain.current.status !== 'Shutoff'}
		<div class="flex h-10 w-full items-center gap-2 border-b p-2">
			{#if vm.current.vncEnabled && vm.current.serial}
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

			{#if consoleType === 'serial' && vm.current.serial}
				{#if connected}
					<Button
						size="sm"
						class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
						onclick={disconnectSerial}
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
						onclick={reconnectSerial}
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
			{/if}
		</div>
	{/if}

	{#if domain.current.status !== 'Shutoff'}
		{#if consoleType === 'vnc' && vm.current.vncEnabled}
			<div class="relative flex min-h-0 flex-1 flex-col">
				<iframe
					class="w-full flex-1 transition-opacity duration-500"
					class:opacity-0={vncLoading}
					class:opacity-100={!vncLoading}
					src={`/vnc/vnc.html?path=${vncPath}&password=${vm.current.vncPassword}&resize=scale&show_dot=true&theme=${mode.current}`}
					title="VM Console"
				></iframe>
				{#if vncLoading}
					<div class="bg-background/50 absolute inset-0 z-10 flex items-center justify-center">
						<span class="icon-[mdi--loading] text-primary h-10 w-10 animate-spin"></span>
					</div>
				{/if}
			</div>
		{:else if consoleType === 'serial' && vm.current.serial}
			<div class="flex h-full w-full flex-col" transition:fade|global={{ duration: 200 }}>
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
					aria-label="VM serial terminal"
					tabindex="-1"
					style:background-color={theme.current.background}
					style="outline: none;"
					bind:this={terminalContainer}
					onpointerdown={() => terminal?.focus()}
				></div>
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

<Dialog.Root bind:open={openSettings}>
	<Dialog.Content class="min-w-[180px]">
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				Console settings - {vm.current?.name}
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
