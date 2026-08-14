<script lang="ts">
	import { Xterm, XtermAddon } from '@battlefieldduck/xterm-svelte';
	import type {
		ITerminalInitOnlyOptions,
		ITerminalOptions,
		Terminal
	} from '@battlefieldduck/xterm-svelte';
	import { demoHostTerminal, type DemoHostTerminalStatus } from '$lib/demo/host-terminal';
	import { onDestroy, onMount } from 'svelte';

	let { node, jailName }: { node: string; jailName: string } = $props();
	let terminal = $state<Terminal>();
	let wrapper = $state<HTMLElement | null>(null);
	let status = $state<DemoHostTerminalStatus>('idle');
	let statusText = $state('FreeBSD jail console');
	let progress = $state(0);
	let detachTerminal: (() => void) | null = null;
	let unsubscribe: (() => void) | null = null;
	let observer: ResizeObserver | null = null;
	let fitAddon: InstanceType<Awaited<ReturnType<typeof XtermAddon.FitAddon>>['FitAddon']> | null =
		null;

	const options: ITerminalOptions & ITerminalInitOnlyOptions = {
		cursorBlink: true,
		cursorStyle: 'bar',
		scrollback: 10000,
		fontFamily: 'Monaco, Menlo, "Courier New", monospace',
		fontSize: 14,
		theme: { background: '#282c34', foreground: '#ffffff' }
	};

	function sessionHostname(value: string) {
		return (
			value
				.trim()
				.toLowerCase()
				.replace(/[^a-z0-9.-]/g, '-') || 'jail'
		);
	}

	function fit() {
		if (!terminal || !fitAddon) return;
		try {
			fitAddon.fit();
			demoHostTerminal.resize(terminal.cols, terminal.rows);
		} catch {
			return;
		}
	}

	async function onLoad(instance: Terminal) {
		terminal = instance;
		fitAddon = new (await XtermAddon.FitAddon()).FitAddon();
		instance.loadAddon(fitAddon);
		demoHostTerminal.setHostname(sessionHostname(jailName));
		detachTerminal = demoHostTerminal.attach(instance);
		requestAnimationFrame(() => requestAnimationFrame(fit));
	}

	onMount(() => {
		demoHostTerminal.setHostname(sessionHostname(jailName));
		unsubscribe = demoHostTerminal.subscribe((snapshot) => {
			status = snapshot.status;
			statusText = snapshot.text;
			progress = snapshot.progress;
		});
		if (wrapper) {
			observer = new ResizeObserver(fit);
			observer.observe(wrapper);
		}
	});

	onDestroy(() => {
		observer?.disconnect();
		unsubscribe?.();
		detachTerminal?.();
		demoHostTerminal.setHostname(node);
		terminal?.dispose?.();
	});
</script>

<div class="flex h-full min-h-0 w-full flex-col bg-[#282c34] text-zinc-100">
	<div
		class="flex min-h-10 shrink-0 flex-wrap items-center gap-2 border-b border-white/10 px-3 py-2"
	>
		<span
			class="h-2 w-2 rounded-full"
			class:bg-emerald-400={status === 'running'}
			class:bg-amber-400={status === 'loading' || status === 'idle'}
			class:bg-red-400={status === 'error'}
		></span>
		<span class="text-xs text-zinc-300">{jailName} · FreeBSD jail session</span>
		<span class="ml-auto text-[11px] text-zinc-500">shared host kernel</span>
	</div>

	{#if status !== 'running'}
		<div class="shrink-0 border-b border-white/10 bg-black/15 px-3 py-2 text-xs text-zinc-400">
			<div class="flex items-center justify-between gap-3">
				<span class="truncate">{statusText}</span>
				{#if status === 'loading'}<span>{progress}%</span>{/if}
			</div>
			{#if status === 'loading'}
				<div class="mt-2 h-px overflow-hidden bg-white/10">
					<div class="h-full bg-zinc-300 transition-[width]" style:width={`${progress}%`}></div>
				</div>
			{/if}
		</div>
	{/if}

	<div bind:this={wrapper} class="terminal-wrapper min-h-0 flex-1 overflow-hidden">
		<Xterm
			class="h-full w-full caret-transparent focus:outline-none"
			style="outline: none;"
			role="application"
			aria-label={`${jailName} demo jail terminal`}
			tabindex={-1}
			{options}
			bind:terminal
			{onLoad}
			onData={(data) => demoHostTerminal.send(data)}
			onpointerdown={() => terminal?.focus()}
		/>
	</div>
</div>

<style>
	:global(.terminal-wrapper .xterm) {
		height: 100%;
	}

	:global(.terminal-wrapper .xterm-viewport) {
		background-color: transparent !important;
	}
</style>
