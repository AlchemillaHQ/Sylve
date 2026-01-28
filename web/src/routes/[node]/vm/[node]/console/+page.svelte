<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import { storage } from '$lib';
	import type { VM, VMDomain } from '$lib/types/vm/vm';
	import { toHex } from '$lib/utils/string';
	import { init as initGhostty, Terminal as GhosttyTerminal } from 'ghostty-web';
	import { onDestroy, onMount, tick } from 'svelte';
	import { getVmById, getVMDomain } from '$lib/api/vm/vm';
	import { updateCache } from '$lib/utils/http';
	import { resource, useInterval, watch } from 'runed';
	import { mode } from 'mode-watcher';

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

	useInterval(() => 1000, {
		callback: () => {
			if (!storage.idle) {
				vm.refetch();
				domain.refetch();
			}
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

	const wssAuth = $state({
		hash: data.hash,
		hostname: storage.hostname || '',
		token: storage.clusterToken || ''
	});

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
	let vncPath = $derived.by(() => {
		return vm.current.vncEnabled
			? `/api/vnc/${encodeURIComponent(String(vm.current.vncPort))}?auth=${toHex(JSON.stringify(wssAuth))}`
			: '';
	});

	let vncLoading = $state(false);
	function startVncLoading() {
		if (!vm.current.vncEnabled) return;
		vncLoading = true;
		setTimeout(() => (vncLoading = false), 1500);
	}

	onMount(() => {
		if (consoleType === 'vnc' && vm.current.vncEnabled) {
			startVncLoading();
		}
	});

	let prevConsoleType: ConsoleType = consoleType;
	$effect(() => {
		if (prevConsoleType !== consoleType) {
			prevConsoleType = consoleType;

			if (consoleType === 'vnc' && vm.current.vncEnabled) {
				localStorage.setItem(`vm-${vm.current.rid}-console-preferred`, 'vnc');
				startVncLoading();
				serialConnected = false;
				// Disconnect serial when switching to VNC
				if (ws) {
					sendKill();
					ws.close();
					ws = null;
				}
				terminal?.dispose?.();
				terminal = null;
			} else if (consoleType === 'serial' && vm.current.serial) {
				localStorage.setItem(`vm-${vm.current.rid}-console-preferred`, 'serial');
				// Auto-connect serial when switching to it
				tick().then(() => {
					serialConnect();
				});
			}
		}
	});

	let terminal = $state<GhosttyTerminal | null>(null);
	let serialEl: HTMLDivElement | null = null;
	let ro: ResizeObserver | null = null;
	let lastWidth = 0;
	let lastHeight = 0;

	let ws: WebSocket | null = null;
	let serialLoading = $state(false);

	function isOpen(w: WebSocket | null): boolean {
		return !!w && w.readyState === WebSocket.OPEN;
	}

	let serialConnected = $state(false);
	let destroyed = $state(false);

	function sendKill(sessionId?: string) {
		if (!isOpen(ws)) return;
		serialConnected = false;
		const body = JSON.stringify({ kill: sessionId ?? '' });
		const payload = new TextEncoder().encode('\x02' + body);
		try {
			ws!.send(payload);
		} catch {}
	}

	function resizeTerminal(width: number, height: number) {
		if (!terminal) return;

		const root = terminal.element as HTMLElement | undefined;
		if (!root) return;

		const canvas = root.querySelector('canvas') as HTMLCanvasElement | null;
		if (!canvas) return;

		const cols = terminal.cols || 80;
		const rows = terminal.rows || 24;

		const cellWidth = canvas.clientWidth / cols || 8;
		const cellHeight = canvas.clientHeight / rows || 16;

		const newCols = Math.max(2, Math.floor(width / cellWidth));
		const newRows = Math.max(2, Math.floor(height / cellHeight));

		terminal.resize(newCols, newRows);
		ws?.send(new TextEncoder().encode('\x01' + JSON.stringify({ cols: newCols, rows: newRows })));
	}

	async function serialConnect() {
		if (!vm.current.serial || !serialEl) return;

		serialLoading = true;

		await initGhostty();
		if (destroyed) return;

		terminal?.dispose?.();
		terminal = new GhosttyTerminal({
			cursorBlink: true,
			fontFamily: 'Monaco, Menlo, "Courier New", monospace',
			fontSize: 14
		});

		terminal.open(serialEl);

		const url = `/api/vm/console?rid=${vm.current.rid}&hash=${data.hash}`;
		ws = new WebSocket(url);
		ws.binaryType = 'arraybuffer';

		ws.onopen = () => {
			serialConnected = true;
			serialLoading = false;

			const rect = serialEl!.getBoundingClientRect();
			lastWidth = rect.width;
			lastHeight = rect.height;
			resizeTerminal(rect.width, rect.height);

			ro?.disconnect();
			ro = new ResizeObserver((entries) => {
				const { width, height } = entries[0].contentRect;
				lastWidth = width;
				lastHeight = height;
				resizeTerminal(width, height);
			});
			ro.observe(serialEl!);
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

		ws.onclose = ws.onerror = () => {
			serialLoading = false;
			serialConnected = false;
		};
	}

	onMount(() => {
		if (consoleType === 'serial' && vm.current.serial) {
			tick().then(() => {
				serialConnect();
			});
		}
	});

	onDestroy(() => {
		destroyed = true;
		ws?.close();
		terminal?.dispose?.();
		ro?.disconnect();
	});
</script>

<div class="flex h-full min-h-0 w-full flex-col">
	<!-- Header: show only if at least one console is available -->
	{#if (vm.current.vncEnabled || vm.current.serial) && domain.current.status !== 'Shutoff'}
		<div class="flex h-10 w-full items-center gap-2 border-b p-2">
			<!-- Switcher: show only if BOTH consoles exist -->
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

			<!-- Serial control: only when Serial is selected and available -->
			{#if consoleType === 'serial' && vm.current.serial}
				<Button
					size="sm"
					variant="outline"
					class="h-6.5"
					disabled={serialLoading}
					onclick={() => {
						if (serialConnected) {
							sendKill();
							ws?.close();
							ws = null;
							terminal?.dispose?.();
							terminal = null;
							serialConnected = false;
						} else {
							serialConnect();
						}
					}}
				>
					<div class="flex items-center gap-2">
						<span class={`icon-[${serialConnected ? 'mdi--power' : 'mdi--refresh'}] h-4 w-4`}
						></span>
						<span>{serialConnected ? 'Kill Serial Session' : 'Reconnect Serial'}</span>
					</div>
				</Button>
			{/if}
		</div>
	{/if}

	{#if data.domain && data.domain.status !== 'Shutoff'}
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
			<div class="relative flex min-h-0 flex-1 flex-col">
				<div
					bind:this={serialEl}
					class="h-full w-full bg-black"
					tabindex="0"
					style="outline: none;"
				/>

				{#if serialLoading}
					<div class="bg-background/50 absolute inset-0 z-10 flex items-center justify-center">
						<span class="icon-[mdi--loading] text-primary h-10 w-10 animate-spin"></span>
					</div>
				{/if}
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
