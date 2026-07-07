<script lang="ts">
	import { page } from '$app/state';
	import { Button } from '$lib/components/ui/button/index.js';
	import { storage } from '$lib';
	import { vmPowerSignal } from '$lib/stores/api.svelte';
	import type { VM, VMDomain } from '$lib/types/vm/vm';
	import { toHex } from '$lib/utils/string';
	import { Xterm, XtermAddon } from '@battlefieldduck/xterm-svelte';
	import type {
		ITerminalOptions,
		ITerminalInitOnlyOptions,
		Terminal
	} from '@battlefieldduck/xterm-svelte';
	import { onMount, getContext } from 'svelte';
	import { getVmById } from '$lib/api/vm/vm';
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
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import ColorPicker from 'svelte-awesome-color-picker';
	import { swatches } from '$lib/utils/terminal';
	import { sleep } from '$lib/utils';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { isMac } from '$lib/hooks/is-mac.svelte';

	type ConsoleType = 'vnc' | 'serial' | 'none';
	type FitAddonInstance = InstanceType<Awaited<ReturnType<typeof XtermAddon.FitAddon>>['FitAddon']>;

	interface Data {
		vm: VM;
		rid: string;
		hash: string;
	}

	let { data }: { data: Data } = $props();

	const domain = getContext<{ current: VMDomain | null; refetch(): void }>('vmDomain');

	// svelte-ignore state_referenced_locally
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

	// svelte-ignore state_referenced_locally
	let cState = new PersistedState(`vm-${data.rid}-console-state`, false);

	// svelte-ignore state_referenced_locally
	let theme = new PersistedState(`vm-${data.rid}-console-theme`, {
		background: '#282c34',
		foreground: '#FFFFFF',
		fontSize: 14
	});

	let fontSizeBindable: number = $state(theme.current.fontSize || 14);
	let bgThemeBindable: string = $state(theme.current.background || '#282c34');
	let fgThemeBindable: string = $state(theme.current.foreground || '#FFFFFF');
	let openSettings = $state(false);

	let terminal = $state<Terminal>();
	let fitAddon: FitAddonInstance | null = null;
	let ws = $state<WebSocket | null>(null);
	let serialConnectionState = $state<'disconnected' | 'connecting' | 'connected'>('disconnected');
	let wrapper = $state<HTMLElement | null>(null);
	let connectionToken = 0;
	let destroyed = $state(false);

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

	useInterval(() => 1000, {
		callback: () => {
			if (!storage.visible) return;
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

	let showConsoleToolbar = $derived(
		!!domain.current &&
			domain.current.status !== 'Shutoff' &&
			((vm.current.vncEnabled && vm.current.serial) ||
				(consoleType === 'serial' && vm.current.serial))
	);

	function sendSize(cols: number, rows: number) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(new TextEncoder().encode('\x01' + JSON.stringify({ cols, rows })));
	}

	function isSerialSocketActive() {
		return serialConnectionState === 'connected' || serialConnectionState === 'connecting';
	}

	function cleanupSerial(forceKill = false) {
		connectionToken += 1;
		serialConnectionState = 'disconnected';

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

	function disconnectSerial() {
		cState.current = true;
		cleanupSerial(true);
	}

	function disconnectSerialForStateChange() {
		cState.current = false;
		cleanupSerial(false);
	}

	function reconnectSerial() {
		if (isSerialSocketActive()) return;
		cState.current = false;
		if (!terminal) return;
		serialConnect();
	}

	async function refetchUntilDomainStatus(targetStatus: 'running' | 'shutoff', attempts = 10) {
		for (let i = 0; i < attempts; i += 1) {
			await Promise.all([vm.refetch(), domain.refetch()]);
			if (
				String(domain.current?.status || '')
					.trim()
					.toLowerCase() === targetStatus
			) {
				return true;
			}

			if (i < attempts - 1) {
				await sleep(500);
			}
		}

		return (
			String(domain.current?.status || '')
				.trim()
				.toLowerCase() === targetStatus
		);
	}

	useResizeObserver(
		() => wrapper,
		() => {
			fitAndSend();
		}
	);

	function serialConnect() {
		if (destroyed || !terminal) return;
		if (!vm.current.serial) return;
		if (!domain.current || domain.current.status === 'Shutoff') return;
		if (isSerialSocketActive()) return;

		cState.current = false;

		const wssAuth = getWSSAuth();
		const url = `/api/vm/console?rid=${vm.current.rid}&auth=${encodeURIComponent(toHex(JSON.stringify(wssAuth)))}`;

		const activeConnectionToken = ++connectionToken;
		const activeTerminal = terminal;
		const socket = new WebSocket(url);
		socket.binaryType = 'arraybuffer';
		ws = socket;
		serialConnectionState = 'connecting';

		socket.onopen = () => {
			if (destroyed || activeConnectionToken !== connectionToken || terminal !== activeTerminal)
				return;

			serialConnectionState = 'connected';

			console.log(`Serial console connected for VM ${data.rid}`);
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
			serialConnectionState = 'disconnected';
		};
	}

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
				if (
					consoleType === 'serial' &&
					vm.current.serial &&
					domain.current?.status !== 'Shutoff' &&
					!cState.current &&
					!isSerialSocketActive()
				) {
					serialConnect();
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

	onMount(() => {
		window.addEventListener('beforeunload', handleBeforeUnload);

		if (consoleType === 'vnc' && vm.current.vncEnabled) {
			startVncLoading();
		}

		return () => {
			window.removeEventListener('beforeunload', handleBeforeUnload);
			destroyed = true;
			connectionToken += 1;
			serialConnectionState = 'disconnected';

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

	watch(
		() => consoleType,
		(type) => {
			if (type === 'vnc' && vm.current.vncEnabled) {
				localStorage.setItem(`vm-${vm.current.rid}-console-preferred`, 'vnc');
				startVncLoading();
				cleanupSerial(false);
			} else if (type === 'serial' && vm.current.serial) {
				localStorage.setItem(`vm-${vm.current.rid}-console-preferred`, 'serial');
			}
		},
		{ lazy: true }
	);

	watch(
		() =>
			String(domain.current?.status || '')
				.trim()
				.toLowerCase(),
		(status, previousStatus) => {
			if (status === 'shutoff') {
				disconnectSerialForStateChange();
				return;
			}

			if (status === 'running') {
				if (consoleType === 'serial' && vm.current.serial && !cState.current) {
					reconnectSerial();
				}

				if (consoleType === 'vnc' && vm.current.vncEnabled && previousStatus !== 'running') {
					startVncLoading();
				}
			}
		},
		{ lazy: true }
	);

	watch(
		() => vmPowerSignal.token,
		() => {
			void (async () => {
				if (vmPowerSignal.rid !== Number(data.rid)) return;

				if (vmPowerSignal.action === 'stop' || vmPowerSignal.action === 'shutdown') {
					disconnectSerialForStateChange();
					await refetchUntilDomainStatus('shutoff');
					return;
				}

				if (vmPowerSignal.action === 'start' || vmPowerSignal.action === 'reboot') {
					const isRunning = await refetchUntilDomainStatus('running');
					if (!isRunning) return;

					if (consoleType === 'serial' && vm.current.serial) {
						cState.current = false;
						reconnectSerial();
					} else if (consoleType === 'vnc' && vm.current.vncEnabled) {
						startVncLoading();
					}
				}
			})();
		},
		{ lazy: true }
	);
</script>

<div class="flex h-full w-full flex-col">
	{#if showConsoleToolbar}
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
				{#if serialConnectionState === 'connected'}
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
						disabled={serialConnectionState === 'connecting'}
						onclick={reconnectSerial}
					>
						<div class="flex items-center gap-2">
							<span class="icon-[mdi--refresh] h-4 w-4"></span>
							<span>{serialConnectionState === 'connecting' ? 'Connecting...' : 'Reconnect'}</span>
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
			{/if}
		</div>
	{/if}

	{#if domain.current?.status !== 'Shutoff'}
		{#if consoleType === 'vnc' && vm.current.vncEnabled}
			<div class="relative flex min-h-0 w-full flex-1 flex-col">
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
			<div class="flex min-h-0 w-full flex-1 flex-col">
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
						aria-label="VM serial terminal"
						tabindex={-1}
						{options}
						bind:terminal
						{onLoad}
						{onData}
						onpointerdown={() => terminal?.focus()}
					/>
				</div>
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
	<Dialog.Content
		class="min-w-45"
		showResetButton={true}
		onReset={() => {
			fontSizeBindable = theme.current.fontSize || 14;
			bgThemeBindable = '#282c34';
			fgThemeBindable = '#FFFFFF';
		}}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center gap-2">
					<SpanWithIcon
						icon="icon-[tdesign--ai-terminal]"
						size="w-6 h-6"
						gap="gap-2"
						title={`Console settings - ${vm.current?.name || ''}`}
					/>
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
