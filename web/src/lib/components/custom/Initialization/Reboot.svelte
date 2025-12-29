<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import { toast } from 'svelte-sonner';
	import { mode } from 'mode-watcher';
	import { getBasicHealth, rebootSystem } from '$lib/api/system/system';
	import { goto } from '$app/navigation';

	let rebootInitiated = $state(false);

	async function waitForRebootCycle({ intervalMs = 2000, timeoutMs = 60 * 60 * 1000 } = {}) {
		const start = Date.now();
		let wentDown = false;

		while (Date.now() - start < timeoutMs) {
			try {
				const health = await getBasicHealth();

				if (!wentDown) {
					// still up → wait for it to go down
					await new Promise((r) => setTimeout(r, intervalMs));
					continue;
				}

				if (health?.status === 'success') {
					return true; // back up AFTER going down
				}
			} catch {
				// request failed → system is down
				wentDown = true;
			}

			await new Promise((r) => setTimeout(r, intervalMs));
		}

		throw new Error('Reboot cycle not completed in time');
	}

	async function handleReboot() {
		rebootInitiated = true;

		try {
			await rebootSystem();
		} catch {}

		const rebootPromise = waitForRebootCycle();

		toast.promise(rebootPromise, {
			loading: 'System is restarting…',
			success: 'System is back online',
			error: 'System did not come back online in time',
			position: 'bottom-center'
		});

		rebootPromise.then(() => {
			goto('/datacenter/summary', { replaceState: true });
		});
	}
</script>

<div class="min-h-screen flex items-center justify-center p-8">
	<div class="text-center space-y-8">
		<div class="flex items-center justify-center gap-4">
			<img
				src={mode.current === 'dark' ? '/logo/white.svg' : '/logo/black.svg'}
				alt="Sylve Logo"
				class="h-8 w-auto opacity-90"
			/>
			<span class="text-lg font-light tracking-[.4em] opacity-90">SYLVE</span>
			<div
				class="w-6 h-6 bg-green-100 dark:bg-green-900/30 rounded-full flex items-center justify-center"
			>
				<span
					class="icon icon-[mdi--check] w-4 h-4 bg-green-600 dark:bg-green-400 rounded-full block"
				></span>
			</div>
			<span class="text-lg font-medium">Setup Complete</span>
		</div>

		<!-- Content -->
		<div class="space-y-6">
			<p class="text-muted-foreground text-sm max-w-xs mx-auto">
				We need to restart the system to apply changes
			</p>

			<Button onclick={handleReboot} class="px-8 py-2.5" disabled={rebootInitiated}>
				{#if rebootInitiated}
					<span class="icon icon-[mdi--loading] w-4 h-4 mr-2 inline-block animate-spin"></span>
					Restarting...
				{:else}
					<span class="icon icon-[mdi--restart] w-4 h-4 mr-2 inline-block"></span>
					Restart
				{/if}
			</Button>
		</div>
	</div>
</div>
