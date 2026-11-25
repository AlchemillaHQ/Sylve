<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import { nodePath } from '$lib/api/common';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import type { DemoHostTerminalStatus } from '$lib/demo/host-terminal';
	import { isDemoMode } from '$lib/demo/runtime';
	import { isMac } from '$lib/hooks/is-mac.svelte';
	import { sha256, toHex } from '$lib/utils/string';
	import {
		Xterm,
		XtermAddon,
		type ITerminalInitOnlyOptions,
		type ITerminalOptions,
		type Terminal
	} from '@battlefieldduck/xterm-svelte';
	import { PersistedState, useResizeObserver } from 'runed';
	import { onMount } from 'svelte';
	import ColorInput from './ColorInput.svelte';
	import { createHostConnection, type ConnectionStatus } from './connection';

	type DemoRuntime = (typeof import('$lib/demo/host-terminal'))['demoHostTerminal'];
	type FitAddon = InstanceType<Awaited<ReturnType<typeof XtermAddon.FitAddon>>['FitAddon']>;

	let { node }: { node: string } = $props();
	const id = $props.id();
	const disconnected = new PersistedState(
		isDemoMode ? 'demo-host-console-state' : 'host-console-state',
		false
	);
	const theme = new PersistedState('host-console-theme', {
		background: '#282c34',
		foreground: '#FFFFFF',
		fontSize: 14
	});

	let terminal = $state<Terminal>();
	let wrapper = $state<HTMLElement | null>(null);
	let ready = $state(false);
	let status = $state<ConnectionStatus>(disconnected.current ? 'disconnected' : 'connecting');
	let dimensions = $state({ cols: 80, rows: 24 });
	let openSettings = $state(false);
	let fontSize = $state<number | undefined>(theme.current.fontSize || 14);
	let background = $state(theme.current.background || '#282c34');
	let foreground = $state(theme.current.foreground || '#FFFFFF');
	let initializationError = $state('');
	let demoStatus: DemoHostTerminalStatus = 'idle';
	let demoText = $state('');
	let demoProgress = $state(0);
	let demoRuntime: DemoRuntime | null = null;
	let detachDemo: (() => void) | null = null;
	let unsubscribeDemo: (() => void) | null = null;
	let demoAttempt = 0;
	let fitAddon: FitAddon | null = null;
	let fitFrame = 0;
	let destroyed = false;
	let active = $derived(status === 'connected' || status === 'connecting');

	const options: ITerminalOptions & ITerminalInitOnlyOptions = {
		cursorBlink: true,
		cursorStyle: 'bar',
		scrollback: 10000,
		fontFamily: 'Monaco, Menlo, "Courier New", monospace',
		fontSize: theme.current.fontSize || 14,
		disableStdin: true,
		theme: { background: theme.current.background, foreground: theme.current.foreground }
	};

	const connection = createHostConnection({
		getUrl: async () => {
			const hostname = node;
			const hash = await sha256(storage.token || '', 1);
			const auth = encodeURIComponent(toHex(JSON.stringify({ hash })));
			return `${nodePath('/api/info/terminal', hostname)}?auth=${auth}`;
		},
		onStatus: updateStatus,
		onOutput: (data) => terminal?.write(data)
	});

	function updateStatus(next: ConnectionStatus) {
		if (destroyed) return;
		status = next;
		if (terminal) terminal.options.disableStdin = next !== 'connected';
		if (next === 'connected') {
			scheduleFit();
			if (!openSettings) terminal?.focus();
		}
	}

	function fitAndSend() {
		if (destroyed || !terminal || !fitAddon || !wrapper?.clientWidth || !wrapper.clientHeight)
			return;
		fitAddon.fit();
		dimensions = { cols: terminal.cols, rows: terminal.rows };
		if (isDemoMode) {
			if (detachDemo) demoRuntime?.resize(terminal.cols, terminal.rows);
		} else connection.resize(terminal.cols, terminal.rows);
	}

	function scheduleFit() {
		cancelAnimationFrame(fitFrame);
		fitFrame = requestAnimationFrame(() => {
			fitFrame = 0;
			fitAndSend();
		});
	}

	useResizeObserver(() => wrapper, scheduleFit);

	function setFontSize(value: number | undefined) {
		const size = Math.max(
			8,
			Math.min(24, Math.round(value !== undefined && Number.isFinite(value) ? value : 14))
		);
		fontSize = size;
		theme.current.fontSize = size;
		if (terminal) terminal.options.fontSize = size;
		scheduleFit();
	}

	function applyColors() {
		theme.current.background = background;
		theme.current.foreground = foreground;
		if (terminal) terminal.options.theme = { background, foreground };
	}

	function syncDemoStatus() {
		if (!detachDemo || destroyed) return;
		updateStatus(
			demoStatus === 'running' ? 'connected' : demoStatus === 'error' ? 'error' : 'connecting'
		);
	}

	async function reconnect() {
		if (destroyed || !ready || !terminal) return;
		disconnected.current = false;
		terminal.reset();
		if (!isDemoMode) {
			void connection.connect();
			return;
		}

		const attempt = ++demoAttempt;
		detachDemo?.();
		detachDemo = null;
		updateStatus('connecting');
		try {
			const { demoHostTerminal: runtime } = await import('$lib/demo/host-terminal');
			if (destroyed || attempt !== demoAttempt || !terminal) return;
			demoRuntime = runtime;
			unsubscribeDemo ??= runtime.subscribe((snapshot) => {
				demoStatus = snapshot.status;
				demoText = snapshot.text;
				demoProgress = snapshot.progress;
				syncDemoStatus();
			});
			runtime.setHostname(node);
			detachDemo = runtime.attach(terminal);
			syncDemoStatus();
			scheduleFit();
		} catch {
			if (destroyed || attempt !== demoAttempt) return;
			demoText = 'Could not load the demo terminal. Reconnect to try again.';
			updateStatus('error');
		}
	}

	function disconnect() {
		disconnected.current = true;
		if (isDemoMode) {
			demoAttempt += 1;
			detachDemo?.();
			detachDemo = null;
			updateStatus('disconnected');
		} else connection.disconnect();
	}

	async function onLoad(t: Terminal) {
		if (destroyed) {
			t.dispose();
			return;
		}
		terminal = t;
		try {
			const { FitAddon } = await XtermAddon.FitAddon();
			if (destroyed) return;
			fitAddon = new FitAddon();
			t.loadAddon(fitAddon);
			t.attachCustomKeyEventHandler((event) => {
				const modifier = isMac ? event.metaKey : event.ctrlKey;
				const otherModifier = isMac ? event.ctrlKey : event.metaKey;
				if (event.type === 'keydown' && modifier && !otherModifier && !event.altKey) {
					if (event.key === '+' || event.key === '=') {
						event.preventDefault();
						setFontSize((theme.current.fontSize || 14) + 1);
						return false;
					}
					if (event.key === '-' || event.key === '_') {
						event.preventDefault();
						setFontSize((theme.current.fontSize || 14) - 1);
						return false;
					}
				}
				return true;
			});
			ready = true;
			scheduleFit();
			if (!disconnected.current) void reconnect();
		} catch {
			if (destroyed) return;
			initializationError = 'Could not initialize the terminal. Reload this page to try again.';
			updateStatus('error');
		}
	}

	function onData(data: string) {
		if (status !== 'connected') return;
		if (isDemoMode) {
			if (detachDemo) demoRuntime?.send(data);
		} else connection.send(data);
	}

	function clearTerminal() {
		terminal?.clear();
		if (isDemoMode && detachDemo) demoRuntime?.refresh();
		terminal?.focus();
	}

	function beforeUnload(event: BeforeUnloadEvent) {
		if (!isDemoMode && status === 'connected') {
			event.preventDefault();
			event.returnValue = '';
		}
	}

	onMount(() => {
		return () => {
			destroyed = true;
			cancelAnimationFrame(fitFrame);
			connection.dispose();
			demoAttempt += 1;
			detachDemo?.();
			unsubscribeDemo?.();
			terminal?.dispose();
			terminal = undefined;
			fitAddon = null;
		};
	});
</script>

<svelte:window onbeforeunload={beforeUnload} />

<div class="flex h-full min-h-96 flex-col px-4 pt-3 pb-4 md:px-5 md:pb-5">
	<header class="mb-5 flex shrink-0 flex-wrap items-center justify-between gap-3">
		<div class="flex min-w-0 items-center gap-3">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
				aria-hidden="true"><span class="icon-[lucide--terminal] size-5"></span></span
			>
			<h1 class="text-2xl font-semibold tracking-tight">Terminal</h1>
		</div>
		<div class="ml-auto flex items-center gap-2">
			<Button
				variant="outline"
				size="icon"
				class="size-9"
				disabled={!ready || !active}
				onclick={clearTerminal}
				aria-label="Clear terminal"
				title="Clear Terminal"
				><span class="icon-[lucide--eraser] size-4" aria-hidden="true"></span></Button
			>
			<Button
				variant="outline"
				size="icon"
				class="size-9"
				onclick={() => (openSettings = true)}
				aria-label="Terminal settings"
				title="Terminal Settings"
				><span class="icon-[lucide--settings-2] size-4" aria-hidden="true"></span></Button
			>
			{#if active}
				<Button variant="outline" class="h-9 gap-2" onclick={disconnect} disabled={!ready}>
					<span class="icon-[lucide--unplug] size-4" aria-hidden="true"></span>Disconnect
				</Button>
			{:else}
				<Button class="h-9 gap-2" onclick={reconnect} disabled={!ready}>
					<span class="icon-[lucide--refresh-cw] size-4" aria-hidden="true"></span>Reconnect
				</Button>
			{/if}
		</div>
	</header>

	<section
		class="bg-card flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border"
		aria-label="Host console"
	>
		<div class="flex min-h-11 shrink-0 items-center justify-between gap-3 border-b px-4 py-2.5">
			<p class="flex min-w-0 items-center gap-2 text-xs" role="status">
				{#if status === 'connecting'}
					<span
						class="icon-[lucide--loader-circle] text-zen-accent size-3.5 shrink-0 animate-spin motion-reduce:animate-none"
						aria-hidden="true"
					></span>
				{:else}
					<span
						class="size-2 shrink-0 rounded-full"
						class:bg-emerald-500={status === 'connected'}
						class:bg-destructive={status === 'error'}
						class:bg-muted-foreground={status === 'disconnected'}
						aria-hidden="true"
					></span>
				{/if}
				<span class="truncate"
					>{#if status === 'connected'}Connected{:else if status === 'connecting'}{isDemoMode &&
						demoText
							? demoText
							: 'Connecting…'}{:else if status === 'error'}Connection Failed{:else}Disconnected{/if}</span
				>
				{#if isDemoMode && status === 'connecting' && demoProgress > 0}
					<span class="shrink-0 tabular-nums">{demoProgress}%</span>
				{/if}
			</p>
			{#if ready && active}
				<span
					class="text-muted-foreground shrink-0 text-xs tabular-nums"
					aria-label="{dimensions.cols} columns, {dimensions.rows} rows"
					>{dimensions.cols} × {dimensions.rows}</span
				>
			{/if}
		</div>
		<div class="relative min-h-0 flex-1">
			<div
				bind:this={wrapper}
				class="zen-terminal absolute inset-0 overflow-hidden p-3"
				class:invisible={!active}
				inert={!active}
				style:background-color={theme.current.background}
			>
				<Xterm
					class="h-full w-full caret-transparent focus:outline-none"
					style="outline: none;"
					role="application"
					aria-label="Host terminal for {node}"
					tabindex={-1}
					{options}
					bind:terminal
					{onLoad}
					{onData}
					onpointerdown={() => terminal?.focus()}
				/>
			</div>
			{#if !active}
				<div
					class="absolute inset-0 flex flex-col items-center justify-center gap-3 overflow-y-auto p-6 text-center"
				>
					<span
						class="text-muted-foreground size-10 {status === 'error'
							? 'icon-[lucide--unplug]'
							: 'icon-[lucide--terminal]'}"
						aria-hidden="true"
					></span>
					<div class="max-w-md space-y-1.5">
						<h2 class="font-medium">
							{status === 'error' ? 'Unable to Connect' : 'Terminal Disconnected'}
						</h2>
						<p class="text-muted-foreground text-sm">
							{#if initializationError}{initializationError}
							{:else if isDemoMode && status === 'error'}{demoText}
							{:else if isDemoMode}Reconnect to attach to the FreeBSD demo host.
							{:else if status === 'error'}The connection was lost or could not be established.
								Reconnect to start a new host terminal session.
							{:else}Reconnect to start a new host terminal session.{/if}
						</p>
					</div>
				</div>
			{/if}
		</div>
	</section>
</div>

<Dialog.Root bind:open={openSettings}>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-md"
		showCloseButton={false}
		onCloseAutoFocus={(event) => {
			if (status === 'connected') {
				event.preventDefault();
				terminal?.focus();
			}
		}}
	>
		<div class="flex shrink-0 items-center justify-between gap-3 border-b px-4 pt-4 pb-3">
			<Dialog.Header class="min-w-0 gap-1 text-left">
				<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span class="icon-[lucide--settings-2] text-zen-accent size-5 shrink-0" aria-hidden="true"
					></span>
					Terminal Settings
				</Dialog.Title>
				<Dialog.Description class="sr-only"
					>Adjust this browser's terminal appearance. Changes are saved automatically.</Dialog.Description
				>
			</Dialog.Header>
			<Dialog.Close>
				{#snippet child({ props })}
					<Button {...props} variant="ghost" size="icon" class="size-9 shrink-0">
						<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
						<span class="sr-only">Close</span>
					</Button>
				{/snippet}
			</Dialog.Close>
		</div>
		<div class="min-h-0 space-y-5 overflow-y-auto p-4">
			<div class="space-y-2">
				<Label for="{id}-font-size">Font Size</Label>
				<Input
					id="{id}-font-size"
					type="number"
					min={8}
					max={24}
					step={1}
					bind:value={fontSize}
					onchange={() => setFontSize(fontSize)}
				/>
				<p class="text-muted-foreground text-xs">
					8–24 px. Use {isMac ? '⌘' : 'Ctrl'} + / − to zoom in the terminal.
				</p>
			</div>
			<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
				<ColorInput bind:value={background} onChange={applyColors} label="Background" />
				<ColorInput bind:value={foreground} onChange={applyColors} label="Foreground" />
			</div>
			<p class="text-muted-foreground text-xs">Changes are saved automatically in this browser.</p>
		</div>
	</Dialog.Content>
</Dialog.Root>

<style>
	:global(.zen-terminal .xterm) {
		height: 100%;
		padding: 0;
	}

	:global(.zen-terminal .xterm-viewport) {
		background-color: transparent !important;
	}
</style>
