<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import type { DemoVMProfile } from '$lib/demo/vm-profiles';
	import {
		getDemoVMRuntime,
		type DemoVMRuntime,
		type DemoVMRuntimeStatus
	} from '$lib/demo/vm-runtime';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { Xterm, XtermAddon } from '@battlefieldduck/xterm-svelte';
	import type {
		ITerminalInitOnlyOptions,
		ITerminalOptions,
		Terminal
	} from '@battlefieldduck/xterm-svelte';
	import { onDestroy, onMount } from 'svelte';

	interface Props {
		profile: DemoVMProfile;
		vmName: string;
		runtimeKey: string;
		view?: 'vga' | 'serial';
		powerToken?: number;
		powerAction?: string;
	}

	let {
		profile,
		vmName,
		runtimeKey,
		view = 'vga',
		powerToken = 0,
		powerAction = ''
	}: Props = $props();

	let vgaViewport = $state<HTMLElement | null>(null);
	let screenMount = $state<HTMLElement | null>(null);
	let serialWrapper = $state<HTMLElement | null>(null);
	let runtime: DemoVMRuntime | null = null;
	let status = $state<DemoVMRuntimeStatus>('idle');
	let statusText = $state('');
	let progress = $state(0);
	let progressDeterminate = $state(false);
	let terminal = $state<Terminal>();
	let terminalReady = false;
	let fitAddon: InstanceType<Awaited<ReturnType<typeof XtermAddon.FitAddon>>['FitAddon']> | null =
		null;
	let serialResizeObserver: ResizeObserver | null = null;
	let vgaResizeObserver: ResizeObserver | null = null;
	let vgaFitFrame = 0;
	let pointerLocked = $state(false);
	let handledPowerToken = 0;
	let detachScreen = () => {};
	let unsubscribeStatus = () => {};
	let unsubscribeSerial = () => {};
	let unsubscribeScreenResize = () => {};

	const terminalOptions: ITerminalOptions & ITerminalInitOnlyOptions = {
		cols: 120,
		rows: 30,
		cursorBlink: true,
		cursorStyle: 'bar',
		scrollback: 10000,
		fontFamily: 'Monaco, Menlo, "Courier New", monospace',
		fontSize: 14,
		theme: { background: '#09090b', foreground: '#f4f4f5' }
	};

	function scheduleFitVga() {
		window.cancelAnimationFrame(vgaFitFrame);
		vgaFitFrame = window.requestAnimationFrame(() => {
			vgaFitFrame = window.requestAnimationFrame(() => {
				if (view === 'vga' && vgaViewport) runtime?.fitScreen(vgaViewport);
			});
		});
	}

	function fitSerial() {
		if (!terminal || !fitAddon) return;
		try {
			fitAddon.fit();
		} catch {
			return;
		}
	}

	async function onTerminalLoad(instance: Terminal) {
		terminal = instance;
		terminalReady = false;
		fitAddon = new (await XtermAddon.FitAddon()).FitAddon();
		instance.loadAddon(fitAddon);
		requestAnimationFrame(() =>
			requestAnimationFrame(() => {
				fitSerial();
				const transcript = runtime?.getSerialTranscript() || '';
				if (transcript) instance.write(transcript);
				terminalReady = true;
			})
		);
	}

	function onSerialData(data: string) {
		runtime?.sendSerial(data);
	}

	function capturePointer() {
		if (status !== 'running' || view !== 'vga' || !runtime) return;
		runtime.setPointerEnabled(true);
		runtime.lockPointer();
		window.setTimeout(() => {
			if (!pointerLocked && profile.id === 'tinycore-x86') runtime?.setPointerEnabled(false);
		}, 300);
	}

	function handlePointerLockChange() {
		pointerLocked = document.pointerLockElement !== null;
		if (profile.id === 'tinycore-x86') runtime?.setPointerEnabled(pointerLocked);
	}

	onMount(() => {
		if (!screenMount) return;
		const activeRuntime = getDemoVMRuntime(runtimeKey, profile, vmName);
		runtime = activeRuntime;
		detachScreen = activeRuntime.attachScreen(screenMount);
		unsubscribeStatus = activeRuntime.subscribe((snapshot) => {
			status = snapshot.status;
			statusText = snapshot.text;
			progress = snapshot.progress;
			progressDeterminate = snapshot.progressDeterminate;
		});
		unsubscribeSerial = activeRuntime.subscribeSerial((output) => {
			if (terminalReady) terminal?.write(output);
		});
		unsubscribeScreenResize = activeRuntime.subscribeScreenResize(scheduleFitVga);
		document.addEventListener('pointerlockchange', handlePointerLockChange);
		void activeRuntime.start();
		scheduleFitVga();
	});

	$effect(() => {
		if (view === 'serial') requestAnimationFrame(() => requestAnimationFrame(fitSerial));
		else {
			terminalReady = false;
			scheduleFitVga();
		}
	});

	$effect(() => {
		if (!powerToken || powerToken === handledPowerToken || !runtime) return;
		handledPowerToken = powerToken;
		void runtime.handlePowerAction(powerAction);
	});

	$effect(() => {
		if (!serialWrapper) return;
		serialResizeObserver?.disconnect();
		serialResizeObserver = new ResizeObserver(fitSerial);
		serialResizeObserver.observe(serialWrapper);
		return () => serialResizeObserver?.disconnect();
	});

	$effect(() => {
		if (!vgaViewport) return;
		vgaResizeObserver?.disconnect();
		vgaResizeObserver = new ResizeObserver(scheduleFitVga);
		vgaResizeObserver.observe(vgaViewport);
		return () => vgaResizeObserver?.disconnect();
	});

	onDestroy(() => {
		document.removeEventListener('pointerlockchange', handlePointerLockChange);
		window.cancelAnimationFrame(vgaFitFrame);
		serialResizeObserver?.disconnect();
		vgaResizeObserver?.disconnect();
		unsubscribeStatus();
		unsubscribeSerial();
		unsubscribeScreenResize();
		detachScreen();
		terminal?.dispose?.();
		if (pointerLocked) document.exitPointerLock?.();
		runtime = null;
	});
</script>

<svelte:head>
	<meta name="referrer" content="no-referrer" />
</svelte:head>

<div class="flex min-h-0 w-full flex-1 flex-col bg-black text-zinc-100">
	<div
		bind:this={vgaViewport}
		class="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden bg-black"
	>
		<div
			class="relative shrink-0 overflow-hidden bg-black"
			class:hidden={view === 'serial'}
			class:opacity-0={status === 'idle' || status === 'error'}
			aria-label={`${vmName} browser console`}
			role="application"
		>
			<div bind:this={screenMount} class="v86-screen shrink-0 bg-black"></div>
			<button
				type="button"
				class="absolute inset-0 z-10 cursor-inherit border-0 bg-transparent p-0 outline-none"
				class:pointer-events-none={pointerLocked}
				aria-label={`Capture pointer for ${vmName}`}
				title="Capture pointer (Esc to release)"
				onclick={capturePointer}
			></button>
		</div>

		<div
			bind:this={serialWrapper}
			class="terminal-wrapper absolute inset-0 min-h-0 overflow-hidden bg-zinc-950"
			class:hidden={view !== 'serial' || (status !== 'running' && status !== 'paused')}
		>
			{#if view === 'serial' && (status === 'running' || status === 'paused')}
				<Xterm
					class="h-full w-full caret-transparent focus:outline-none"
					style="outline: none;"
					role="application"
					aria-label={`${vmName} browser serial console`}
					tabindex={-1}
					options={terminalOptions}
					bind:terminal
					onLoad={onTerminalLoad}
					onData={onSerialData}
					onpointerdown={() => terminal?.focus()}
				/>
			{/if}
		</div>

		{#if status === 'error'}
			<div class="absolute inset-0 flex items-center justify-center p-6">
				<div class="w-full max-w-md rounded-lg border border-white/10 bg-zinc-900/95 p-5 shadow-xl">
					<div class="mb-4 flex items-start gap-3">
						<div
							class="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-white/10 bg-white/5"
						>
							<span class="icon-[material-symbols--memory-alt-outline] h-5 w-5 text-zinc-300"
							></span>
						</div>
						<div class="min-w-0">
							<h2 class="font-medium">{profile.label}</h2>
							<p class="mt-0.5 text-xs text-zinc-400">
								{profile.release} · {formatBytesBinary(profile.memoryBytes)} RAM
							</p>
						</div>
					</div>
					<p class="text-sm leading-6 text-zinc-300">{profile.description}</p>
					<p
						class="mt-3 rounded border border-red-900/60 bg-red-950/40 px-3 py-2 text-xs text-red-300"
					>
						{statusText}
					</p>
					<Button class="mt-5 h-8 w-full" onclick={() => runtime?.retry()}>Try again</Button>
				</div>
			</div>
		{:else if status === 'idle' || status === 'loading'}
			<div class="absolute inset-0 flex items-center justify-center bg-zinc-950/90 p-6">
				<div class="w-full max-w-sm">
					<div class="mb-3 flex items-center justify-between gap-4 text-xs text-zinc-400">
						<span class="truncate">
							{status === 'idle' ? `Connecting to ${vmName}` : statusText}
						</span>
						{#if progressDeterminate}<span>{progress}%</span>{/if}
					</div>
					<div class="h-1 overflow-hidden rounded-full bg-white/10">
						<div
							class="h-full bg-zinc-200 transition-[width] duration-200"
							class:loading-indeterminate={!progressDeterminate}
							style:width={progressDeterminate ? `${progress}%` : '36%'}
						></div>
					</div>
				</div>
			</div>
		{/if}
	</div>
</div>

<style>
	:global(.terminal-wrapper .xterm) {
		height: 100%;
	}

	:global(.terminal-wrapper .xterm-viewport) {
		background-color: transparent !important;
	}

	.v86-screen :global(canvas) {
		display: block;
	}

	.loading-indeterminate {
		animation: loading-slide 1.1s ease-in-out infinite;
	}

	@keyframes loading-slide {
		from {
			transform: translateX(-120%);
		}
		to {
			transform: translateX(310%);
		}
	}
</style>
