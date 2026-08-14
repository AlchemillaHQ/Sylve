<script lang="ts">
	import { onMount } from 'svelte';
	import { demoHostTerminal } from '$lib/demo/host-terminal';

	let { hostname = 'leto' } = $props<{ hostname?: string }>();
	let screenContainer: HTMLElement;

	type NavigatorHints = Navigator & {
		connection?: { saveData?: boolean; effectiveType?: string };
		deviceMemory?: number;
	};

	function canWarmHost() {
		const hints = navigator as NavigatorHints;
		if (hints.connection?.saveData) return false;
		if (['slow-2g', '2g'].includes(hints.connection?.effectiveType || '')) return false;
		if (hints.deviceMemory !== undefined && hints.deviceMemory < 4) return false;
		return true;
	}

	$effect(() => {
		demoHostTerminal.setHostname(hostname);
	});

	onMount(() => {
		demoHostTerminal.registerScreenContainer(screenContainer);

		let timeout = 0;
		let idleCallback = 0;
		let scheduled = false;
		const start = () => void demoHostTerminal.start();
		const scheduleStart = () => {
			if (scheduled || document.visibilityState !== 'visible' || !canWarmHost()) return;
			scheduled = true;
			if ('requestIdleCallback' in window) {
				idleCallback = window.requestIdleCallback(start, { timeout: 5000 });
			} else {
				timeout = window.setTimeout(start, 1500);
			}
		};

		document.addEventListener('visibilitychange', scheduleStart);
		scheduleStart();

		return () => {
			document.removeEventListener('visibilitychange', scheduleStart);
			if (idleCallback) window.cancelIdleCallback(idleCallback);
			if (timeout) window.clearTimeout(timeout);
			void demoHostTerminal.destroy(screenContainer);
		};
	});
</script>

<svelte:head>
	<meta name="referrer" content="no-referrer" />
</svelte:head>

<div
	bind:this={screenContainer}
	class="pointer-events-none fixed -left-[10000px] top-0 h-[400px] w-[720px] overflow-hidden opacity-0"
	aria-hidden="true"
>
	<div class="font-mono text-sm leading-none whitespace-pre"></div>
	<canvas></canvas>
</div>
