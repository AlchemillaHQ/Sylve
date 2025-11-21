<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import { storage } from '$lib';
	import type { VM, VMDomain } from '$lib/types/vm/vm';
	import { sha256, toHex } from '$lib/utils/string';
	import {
		Xterm,
		XtermAddon,
		type FitAddon,
		type ITerminalInitOnlyOptions,
		type ITerminalOptions,
		type Terminal
	} from '@battlefieldduck/xterm-svelte';
	import { onDestroy, tick } from 'svelte';
	import { useQueries } from '$lib/runes/useQuery.svelte';
	import { getVmById, getVMDomain } from '$lib/api/vm/vm';
	import { updateCache } from '$lib/utils/http';

	type ConsoleType = 'vnc' | 'serial' | 'none';

	interface Data {
		vm: VM;
		domain: VMDomain;
		vmId: string;
		hash: string;
	}

	let { data }: { data: Data } = $props();
	const {
		vm: vmQuery,
		domain: domainQuery,
		refetchAll
	} = useQueries(() => ({
		vm: () => ({
			key: `vm-${data.vmId}`,
			queryFn: () => getVmById(Number(data.vmId), 'vmid'),
			initialData: data.vm,
			onSuccess: (f: VM) => {
				updateCache(`vm-${data.vmId}`, f);
			},
			refetchInterval: 1000
		}),
		domain: () => ({
			key: `vm-domain-${data.vmId}`,
			queryFn: () => getVMDomain(data.vmId),
			initialData: data.domain,
			onSuccess: (f: VMDomain) => {
				updateCache(`vm-domain-${data.vmId}`, f);
			},
			refetchInterval: 1000
		})
	}));

	let vm = $derived(vmQuery.data);
	let domain = $derived(domainQuery.data);

	const wssAuth = $state({
		hash: data.hash,
		hostname: storage.hostname || '',
		token: storage.clusterToken || ''
	});

	function resolveInitialConsole(): ConsoleType {
		const both = vm.vncEnabled && vm.serial;
		const onlyVnc = vm.vncEnabled && !vm.serial;
		const onlySerial = !vm.vncEnabled && vm.serial;

		if (both) {
			const preferred = localStorage.getItem(`vm-${vm.vmId}-console-preferred`);
			if ((preferred === 'vnc' && vm.vncEnabled) || (preferred === 'serial' && vm.serial)) {
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
		return vm.vncEnabled
			? `/api/vnc/${encodeURIComponent(String(vm.vncPort))}?auth=${toHex(JSON.stringify(wssAuth))}`
			: '';
	});

	let vncLoading = $state(false);
	function startVncLoading() {
		if (!vm.vncEnabled) return;
		vncLoading = true;
		setTimeout(() => (vncLoading = false), 1500);
	}

	let prevConsoleType: ConsoleType = consoleType;
	$effect(() => {
		if (prevConsoleType !== consoleType) {
			prevConsoleType = consoleType;

			if (consoleType === 'vnc' && vm.vncEnabled) {
				localStorage.setItem(`vm-${vm.vmId}-console-preferred`, 'vnc');
				startVncLoading();
				serialConnected = false;
			} else if (consoleType === 'serial' && vm.serial) {
				localStorage.setItem(`vm-${vm.vmId}-console-preferred`, 'serial');
			}
		}
	});

	let terminal = $state<Terminal>();
	let fitAddon: FitAddon | null = null;
	let ws: WebSocket | null = null;
	let serialLoading = $state(false);
	let serialEl: HTMLDivElement | null = null;
	let ro: ResizeObserver | null = null;

	const termOptions: ITerminalOptions & ITerminalInitOnlyOptions = {
		cursorBlink: true
	};

	function isOpen(w: WebSocket | null): boolean {
		return !!w && w.readyState === WebSocket.OPEN;
	}

	let serialConnected = $state(false);

	function sendKill(sessionId?: string) {
		if (!isOpen(ws)) return;
		serialConnected = false;
		const body = JSON.stringify({ kill: sessionId ?? '' });
		const payload = new TextEncoder().encode('\x02' + body);
		try {
			ws!.send(payload);
		} catch {}
	}

	async function fitSoon() {
		await tick();
		await new Promise(requestAnimationFrame);
		await new Promise(requestAnimationFrame);
		fitAddon?.fit();
		if (ws && isOpen(ws)) {
			const dims = fitAddon?.proposeDimensions();
			ws.send(
				new TextEncoder().encode('\x01' + JSON.stringify({ rows: dims?.rows, cols: dims?.cols }))
			);
		}
	}

	async function serialConnect() {
		if (!vm.serial) return;

		serialLoading = true;

		const headerProto = toHex(
			JSON.stringify({
				hostname: storage.hostname || '',
				token: storage.clusterToken || ''
			})
		);

		const url = `/api/vm/console?vmid=${vm.vmId}&hash=${data.hash}`;

		if (ws) {
			try {
				ws.close();
			} catch {}
			ws = null;
		}

		ws = new WebSocket(url, [headerProto]);
		ws.binaryType = 'arraybuffer';

		ws.onopen = async () => {
			serialConnected = true;

			const Fit = new (await XtermAddon.FitAddon()).FitAddon();
			fitAddon = Fit;
			terminal?.loadAddon(Fit);

			fitAddon?.fit();
			const dims = fitAddon?.proposeDimensions();
			ws?.send(
				new TextEncoder().encode('\x01' + JSON.stringify({ rows: dims?.rows, cols: dims?.cols }))
			);

			serialLoading = false;

			fitSoon();

			if (ro) {
				ro.disconnect();
				ro = null;
			}
			if (serialEl) {
				ro = new ResizeObserver(() => {
					fitAddon?.fit();
					if (ws) {
						const d = fitAddon?.proposeDimensions();
						ws.send(
							new TextEncoder().encode('\x01' + JSON.stringify({ rows: d?.rows, cols: d?.cols }))
						);
					}
				});
				ro.observe(serialEl);
			}
		};

		ws.onmessage = (e) => {
			if (e.data instanceof ArrayBuffer) {
				terminal?.write(new Uint8Array(e.data));
			}
		};

		ws.onclose = () => {
			serialLoading = false;
			serialConnected = false;
		};

		ws.onerror = () => {
			serialLoading = false;
			serialConnected = false;
		};
	}

	function onTermLoad() {
		serialConnect().then(fitSoon);
	}

	function onTermData(data: string) {
		ws?.send(new TextEncoder().encode('\x00' + data));
	}

	onDestroy(() => {
		try {
			ws?.close();
		} catch {}
		ws = null;
		if (ro) {
			ro.disconnect();
			ro = null;
		}
		fitAddon = null;
	});
</script>

<div class="flex h-full min-h-0 w-full flex-col">
	<!-- Header: show only if at least one console is available -->
	{#if (vm.vncEnabled || vm.serial) && domain.status !== 'Shutoff'}
		<div class="flex h-10 w-full items-center gap-2 border-b p-2">
			<!-- Switcher: show only if BOTH consoles exist -->
			{#if vm.vncEnabled && vm.serial}
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
			{#if consoleType === 'serial' && vm.serial}
				<Button
					size="sm"
					variant="outline"
					class="h-6.5"
					disabled={serialLoading}
					onclick={() => {
						if (serialConnected) {
							sendKill();
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
		{#if consoleType === 'vnc' && vm.vncEnabled}
			<div class="relative flex min-h-0 flex-1 flex-col">
				<iframe
					class="w-full flex-1 transition-opacity duration-500"
					class:opacity-0={vncLoading}
					class:opacity-100={!vncLoading}
					src={`/vnc/vnc.html?path=${vncPath}&password=${vm.vncPassword}`}
					title="VM Console"
				/>
				{#if vncLoading}
					<div class="bg-background/50 absolute inset-0 z-10 flex items-center justify-center">
						<span class="icon-[mdi--loading] text-primary h-10 w-10 animate-spin"></span>
					</div>
				{/if}
			</div>
		{:else if consoleType === 'serial' && vm.serial}
			<div bind:this={serialEl} class="relative flex min-h-0 flex-1 flex-col">
				<Xterm
					bind:terminal
					options={termOptions}
					onLoad={onTermLoad}
					onData={onTermData}
					class="h-full w-full"
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
