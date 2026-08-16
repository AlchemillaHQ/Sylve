<script lang="ts">
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
	import { onMount, getContext, untrack, type Component } from 'svelte';
	import { getVmByIdResult } from '$lib/api/vm/vm';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
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
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import ColorPicker from 'svelte-awesome-color-picker';
	import { swatches } from '$lib/utils/terminal';
	import { sleep } from '$lib/utils';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { isMac } from '$lib/hooks/is-mac.svelte';
	import { isDemoMode } from '$lib/demo/runtime';
	import type { DemoVMProfile } from '$lib/demo/vm-profiles';

	type ConsoleType = 'vnc' | 'serial' | 'none';
	type FitAddonInstance = InstanceType<Awaited<ReturnType<typeof XtermAddon.FitAddon>>['FitAddon']>;
	type VMConsoleControlState = {
		type: 'control-state';
		hasControl: boolean;
		controllerUsername: string;
		observerCount: number;
	};

	interface Data {
		node: string;
		vm: VM;
		rid: number;
		hash: string;
	}

	let { data }: { data: Data } = $props();
	type ResolveDemoVMProfile = (vm: Pick<VM, 'name' | 'storages'>) => DemoVMProfile | null;
	let resolveDemoVMProfile = $state<ResolveDemoVMProfile | null>(null);
	let DemoVMConsoleComponent = $state<Component<{
		profile: DemoVMProfile;
		vmName: string;
		runtimeKey: string;
		view?: 'vga' | 'serial';
		powerToken?: number;
		powerAction?: string;
	}> | null>(null);
	const initialData = untrack(() => data);
	let consoleIdentity = $derived(`${data.node}\u0000${data.rid}`);

	const domain = getContext<{ current: VMDomain | null; refetch(): void }>('vmDomain');

	type VMConsoleSnapshot = { identity: string; vm: VM };
	const vmResource = resource(
		[() => data.node, () => data.rid],
		async ([hostname, rid], _, { signal }): Promise<VMConsoleSnapshot> => {
			const result = await getVmByIdResult(rid, { hostname, signal });
			if (isAPIResponse(result)) {
				throw new Error(result.message || result.error?.toString() || 'Unable to load VM');
			}
			await updateCache(`vm-${rid}`, result, hostname);
			return { identity: `${hostname}\u0000${rid}`, vm: result };
		},
		{
			lazy: true,
			initialValue: {
				identity: `${initialData.node}\u0000${initialData.rid}`,
				vm: initialData.vm
			}
		}
	);

	const vm = {
		get current(): VM {
			return vmResource.current.identity === consoleIdentity ? vmResource.current.vm : data.vm;
		},
		refetch: () => vmResource.refetch()
	};
	let demoProfile = $derived(
		isDemoMode && resolveDemoVMProfile ? resolveDemoVMProfile(vm.current) : null
	);

	function getWSSAuth() {
		return {
			hash: data.hash,
			hostname: data.node
		};
	}

	function consoleStorageKey(suffix: string): string {
		const scopedKey = `node-${data.node}-vm-${data.rid}-console-${suffix}`;
		if (typeof localStorage !== 'undefined' && localStorage.getItem(scopedKey) === null) {
			const legacyValue = localStorage.getItem(`vm-${data.rid}-console-${suffix}`);
			if (legacyValue !== null) localStorage.setItem(scopedKey, legacyValue);
		}
		return scopedKey;
	}

	function resolveInitialConsole(): ConsoleType {
		const both = vm.current.vncEnabled && vm.current.serial;
		const onlyVnc = vm.current.vncEnabled && !vm.current.serial;
		const onlySerial = !vm.current.vncEnabled && vm.current.serial;

		if (both) {
			const preferred = localStorage.getItem(consoleStorageKey('preferred'));
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

	let cState = $state(new PersistedState(consoleStorageKey('state'), false));

	let theme = $state(
		new PersistedState(consoleStorageKey('theme'), {
			background: '#282c34',
			foreground: '#FFFFFF',
			fontSize: 14
		})
	);

	const initialTheme = untrack(() => ({
		background: theme.current.background || '#282c34',
		foreground: theme.current.foreground || '#FFFFFF',
		fontSize: theme.current.fontSize || 14
	}));
	let fontSizeBindable: number = $state(initialTheme.fontSize);
	let bgThemeBindable: string = $state(initialTheme.background);
	let fgThemeBindable: string = $state(initialTheme.foreground);
	let openSettings = $state(false);

	let terminal = $state<Terminal>();
	let fitAddon: FitAddonInstance | null = null;
	let ws = $state<WebSocket | null>(null);
	let serialConnectionState = $state<'disconnected' | 'connecting' | 'connected'>('disconnected');
	let serialConnectionError = $state(false);
	let hasSerialControl = $state(false);
	let serialControllerUsername = $state('');
	let serialObserverCount = $state(0);
	let takeoverOpen = $state(false);
	let takeoverPending = $state(false);
	let wrapper = $state<HTMLElement | null>(null);
	let connectionToken = 0;
	let destroyed = $state(false);

	const options: ITerminalOptions & ITerminalInitOnlyOptions = {
		cursorBlink: true,
		cursorStyle: 'bar',
		disableStdin: true,
		scrollback: 10000,
		fontFamily: 'Monaco, Menlo, "Courier New", monospace',
		fontSize: initialTheme.fontSize,
		theme: {
			background: initialTheme.background,
			foreground: initialTheme.foreground
		}
	};

	let serialControlLabel = $derived(
		hasSerialControl
			? 'You have control'
			: serialControllerUsername
				? `Controlled by ${serialControllerUsername}`
				: 'View only'
	);

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
	let noVNCSource = $derived.by(() => {
		const params = new URLSearchParams({
			path: vncPath,
			password: vm.current.vncPassword,
			resize: 'scale',
			show_dot: 'true',
			theme: mode.current ?? 'system'
		});
		return `/vnc/vnc.html?${params.toString()}`;
	});

	let vncLoading = $state(false);
	let vncSettling = $state(false);

	function startVncLoading() {
		if (!vm.current.vncEnabled) return;
		vncLoading = true;
		vncSettling = true;

		/*
            The below code is fucking ugly, I know..but I don't know how else we could get rid of the ugly fucking animation that shows when no VNC is just being mounted by Svelte, I have wasted way too much time on this already but feel free to take a crack at it, if you're reading this it's not a backend issue, do not touch the websocket <-> VNC bridge/proxy, if you do I will point at you and laugh.

            Hours wasted counter -> 56

            ^ Don't forget to increment when you're done.
        */

		// Don't mount the iframe until layout is stable
		setTimeout(() => {
			vncLoading = false;

			// We keep the overlay up for a bit longer, even though iframe is mounted now,
			// to hide noVNC's own connect animation/flicker
			setTimeout(() => {
				vncSettling = false;
			}, 800);
		}, 600);
	}

	let normalizedDomainStatus = $derived(
		String(domain.current?.status || '')
			.trim()
			.toLowerCase()
	);
	function consoleStatusAvailable(status: string | null | undefined): boolean {
		return ['running', 'blocked', 'paused', 'shutdown', 'pmsuspended'].includes(
			String(status || '')
				.trim()
				.toLowerCase()
		);
	}
	let isConsoleDomainAvailable = $derived(consoleStatusAvailable(normalizedDomainStatus));
	let showConsoleToolbar = $derived(
		isConsoleDomainAvailable &&
			(isDemoMode
				? vm.current.vncEnabled && vm.current.serial
				: (vm.current.vncEnabled && vm.current.serial) ||
					(consoleType === 'serial' && vm.current.serial))
	);

	function isVMConsoleControlState(value: unknown): value is VMConsoleControlState {
		if (!value || typeof value !== 'object') return false;

		const state = value as Partial<VMConsoleControlState>;
		return (
			state.type === 'control-state' &&
			typeof state.hasControl === 'boolean' &&
			typeof state.controllerUsername === 'string' &&
			typeof state.observerCount === 'number' &&
			Number.isInteger(state.observerCount) &&
			state.observerCount >= 0
		);
	}

	function resetSerialControlState() {
		hasSerialControl = false;
		serialControllerUsername = '';
		serialObserverCount = 0;
		takeoverOpen = false;
		takeoverPending = false;
		if (terminal) terminal.options.disableStdin = true;
	}

	function applySerialControlState(state: VMConsoleControlState) {
		const gainedControl = !hasSerialControl && state.hasControl;

		hasSerialControl = state.hasControl;
		serialControllerUsername = state.controllerUsername;
		serialObserverCount = state.observerCount;
		takeoverPending = false;
		if (state.hasControl) takeoverOpen = false;

		if (terminal) terminal.options.disableStdin = !state.hasControl;
		if (!gainedControl) return;

		requestAnimationFrame(() => {
			requestAnimationFrame(() => {
				fitAndSend();
				terminal?.focus();
			});
		});
	}

	function sendSize(cols: number, rows: number) {
		if (!hasSerialControl) return;
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(new TextEncoder().encode('\x01' + JSON.stringify({ cols, rows })));
	}

	function requestSerialControl() {
		if (hasSerialControl || takeoverPending) return;
		if (!ws || ws.readyState !== WebSocket.OPEN) return;

		takeoverPending = true;
		try {
			ws.send(new Uint8Array([2]));
		} catch {
			takeoverPending = false;
		}
	}

	function isSerialSocketActive() {
		return serialConnectionState === 'connected' || serialConnectionState === 'connecting';
	}

	function cleanupSerial() {
		connectionToken += 1;
		serialConnectionState = 'disconnected';
		serialConnectionError = false;
		resetSerialControlState();

		const socket = ws;
		ws = null;

		if (socket) {
			socket.onopen = null;
			socket.onmessage = null;
			socket.onerror = null;
			socket.onclose = null;
		}

		if (socket && socket.readyState === WebSocket.OPEN) {
			socket.close();
		} else if (socket && socket.readyState === WebSocket.CONNECTING) {
			socket.close();
		}
	}

	function disconnectSerial() {
		cState.current = true;
		cleanupSerial();
	}

	function disconnectSerialForStateChange() {
		cState.current = false;
		cleanupSerial();
	}

	watch(
		() => consoleIdentity,
		(identity, previousIdentity) => {
			if (!previousIdentity || identity === previousIdentity) return;

			cleanupSerial();
			terminal?.reset();
			cState = new PersistedState(consoleStorageKey('state'), false);
			theme = new PersistedState(consoleStorageKey('theme'), {
				background: '#282c34',
				foreground: '#FFFFFF',
				fontSize: 14
			});
			fontSizeBindable = theme.current.fontSize || 14;
			bgThemeBindable = theme.current.background || '#282c34';
			fgThemeBindable = theme.current.foreground || '#FFFFFF';
			if (terminal) {
				terminal.options.fontSize = fontSizeBindable;
				terminal.options.theme = {
					background: bgThemeBindable,
					foreground: fgThemeBindable
				};
			}
			consoleType = resolveInitialConsole();
			if (consoleType === 'vnc' && vm.current.vncEnabled) startVncLoading();
		},
		{ lazy: true }
	);

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
		if (!isConsoleDomainAvailable) return;
		if (isSerialSocketActive()) return;

		cState.current = false;
		serialConnectionError = false;
		resetSerialControlState();

		const wssAuth = getWSSAuth();
		const url = `/api/vm/${encodeURIComponent(String(vm.current.rid))}/console?auth=${encodeURIComponent(toHex(JSON.stringify(wssAuth)))}`;

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
			serialConnectionError = false;
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
			} else if (typeof e.data === 'string') {
				try {
					const message: unknown = JSON.parse(e.data);
					if (isVMConsoleControlState(message)) {
						applySerialControlState(message);
						return;
					}
				} catch {
					// Preserve existing plain-text server messages in the terminal.
				}

				try {
					activeTerminal?.write(e.data);
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
			serialConnectionError = true;
			resetSerialControlState();
		};
	}

	function onData(data: string) {
		if (!hasSerialControl) return;
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
					isConsoleDomainAvailable &&
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
		let cancelled = false;
		window.addEventListener('beforeunload', handleBeforeUnload);

		if (isDemoMode) {
			void Promise.all([
				import('$lib/components/custom/VM/DemoVMConsole.svelte'),
				import('$lib/demo/vm-profiles')
			]).then(([consoleModule, profileModule]) => {
				if (cancelled) return;
				DemoVMConsoleComponent = consoleModule.default;
				resolveDemoVMProfile = profileModule.resolveDemoVMProfile;
			});
		} else if (consoleType === 'vnc' && vm.current.vncEnabled) {
			startVncLoading();
		}

		return () => {
			cancelled = true;
			window.removeEventListener('beforeunload', handleBeforeUnload);
			destroyed = true;
			connectionToken += 1;
			serialConnectionState = 'disconnected';
			resetSerialControlState();

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
				localStorage.setItem(consoleStorageKey('preferred'), 'vnc');
				startVncLoading();
				cleanupSerial();
			} else if (type === 'serial' && vm.current.serial) {
				localStorage.setItem(consoleStorageKey('preferred'), 'serial');
			}
		},
		{ lazy: true }
	);

	watch(
		() => normalizedDomainStatus,
		(status, previousStatus) => {
			if (status === 'shutoff') {
				disconnectSerialForStateChange();
				return;
			}

			if (consoleStatusAvailable(status)) {
				if (consoleType === 'serial' && vm.current.serial && !cState.current) {
					reconnectSerial();
				}

				if (
					consoleType === 'vnc' &&
					vm.current.vncEnabled &&
					!consoleStatusAvailable(previousStatus)
				) {
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
				if (vmPowerSignal.rid !== data.rid) return;

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

	let vncIframe = $state<HTMLIFrameElement | null>(null);

	function nudgeVncResize() {
		// give layout a couple frames to settle, then poke the iframe
		requestAnimationFrame(() => {
			requestAnimationFrame(() => {
				try {
					vncIframe?.contentWindow?.dispatchEvent(new Event('resize'));
					console.log('resize');
				} catch {
					// cross-origin or not ready yet, ignore
				}
			});
		});
	}
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

			{#if !isDemoMode && consoleType === 'serial' && vm.current.serial}
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

				<div class="ml-auto flex min-w-0 items-center gap-2">
					{#if serialConnectionState === 'connected' && serialObserverCount > 0}
						<div
							class="text-muted-foreground flex min-w-0 items-center gap-1.5 text-xs"
							role="status"
							aria-live="polite"
							aria-atomic="true"
							title={`${serialControlLabel} · ${serialObserverCount} connected`}
						>
							<span
								class={`${hasSerialControl ? 'icon-[mdi--keyboard-outline] text-emerald-500' : 'icon-[mdi--eye-outline] text-amber-500'} h-4 w-4 shrink-0`}
								aria-hidden="true"
							></span>
							<span class="max-w-40 truncate">{serialControlLabel}</span>
							<span class="hidden shrink-0 xl:inline">· {serialObserverCount} connected</span>
						</div>

						{#if !hasSerialControl}
							<Button
								variant="outline"
								size="sm"
								class="h-6 shrink-0"
								disabled={takeoverPending}
								onclick={() => {
									takeoverOpen = true;
								}}
								aria-label={takeoverPending
									? 'Taking serial console control'
									: 'Take serial console control'}
							>
								<span
									class={`${takeoverPending ? 'icon-[mdi--loading] animate-spin' : 'icon-[mdi--cursor-default-click-outline]'} h-4 w-4`}
								></span>
								<span class="hidden lg:inline">
									{takeoverPending ? 'Taking control...' : 'Take Control'}
								</span>
							</Button>
						{/if}
					{/if}

					<div class="flex shrink-0 items-center gap-1">
						<Button
							variant="outline"
							size="sm"
							class="h-6"
							aria-label="Clear terminal"
							title="Clear terminal"
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
							class="h-6"
							aria-label="Console settings"
							title="Console settings"
							onclick={() => {
								openSettings = true;
							}}
						>
							<span class="icon-[mdi--cog-outline] h-4 w-4"></span>
						</Button>
					</div>
				</div>
			{/if}
		</div>
	{/if}

	{#if !domain.current}
		<div class="flex flex-1 flex-col items-center justify-center space-y-3 text-center text-base">
			<span class="icon-[mdi--server-network-off] text-primary dark:text-secondary h-14 w-14"
			></span>
			<div class="max-w-md">The VM runtime state is currently unavailable.</div>
		</div>
	{:else if isConsoleDomainAvailable}
		{#if isDemoMode && demoProfile && DemoVMConsoleComponent}
			{#key `${data.node}:${data.rid}:${demoProfile.id}:${vm.current.name}`}
				<DemoVMConsoleComponent
					profile={demoProfile}
					vmName={vm.current.name}
					runtimeKey={String(data.rid)}
					view={consoleType === 'serial' ? 'serial' : 'vga'}
					powerToken={vmPowerSignal.rid === data.rid ? vmPowerSignal.token : 0}
					powerAction={vmPowerSignal.action}
				/>
			{/key}
		{:else if isDemoMode}
			<div class="bg-background flex min-h-0 w-full flex-1 items-center justify-center">
				<span class="icon-[mdi--loading] text-primary h-10 w-10 animate-spin"></span>
			</div>
		{:else if consoleType === 'vnc' && vm.current.vncEnabled}
			<div class="relative flex min-h-0 w-full flex-1 flex-col">
				{#if !vncLoading}
					<iframe class="w-full flex-1" src={noVNCSource} title="VM Console"></iframe>
				{/if}

				{#if vncLoading || vncSettling}
					<div class="bg-background absolute inset-0 z-10 flex items-center justify-center">
						<span class="icon-[mdi--loading] text-primary h-10 w-10 animate-spin"></span>
					</div>
				{/if}
			</div>
		{:else if consoleType === 'serial' && vm.current.serial}
			<div class="flex min-h-0 w-full flex-1 flex-col">
				{#if serialConnectionError && !cState.current}
					<div
						class="border-b border-amber-700/50 bg-amber-950/40 px-3 py-2 text-xs text-amber-200"
					>
						The serial console connection was interrupted. Use Reconnect to try again.
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
				{#if normalizedDomainStatus === 'shutoff'}
					The VM is currently powered off.<br />
					Start the VM to access its console.
				{:else}
					The console is unavailable while the VM is {domain.current.status.toLowerCase()}.
				{/if}
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

<AlertDialog.Root bind:open={takeoverOpen}>
	<AlertDialog.Content class="p-6" onInteractOutside={(event) => event.preventDefault()}>
		<AlertDialog.Header>
			<AlertDialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--console-network-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title="Take serial console control?"
				/>
			</AlertDialog.Title>
			<AlertDialog.Description>
				Taking control immediately makes this browser the active serial input controller
				{#if serialControllerUsername}
					and puts <span class="font-medium">{serialControllerUsername}</span> in view-only mode{/if}.
				All connected admins will continue seeing the same serial output.
			</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel disabled={takeoverPending}>Cancel</AlertDialog.Cancel>
			<AlertDialog.Action onclick={requestSerialControl} disabled={takeoverPending}>
				Take Control
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>

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
