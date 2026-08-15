<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import { toast } from 'svelte-sonner';
	import { mode } from 'mode-watcher';
	import { probeBasicHealth, rebootSystem } from '$lib/api/system/system';
	import { handleAPIError } from '$lib/utils/http';
	import { onMount } from 'svelte';

	let rebootInitiated = $state(false);
	// In a jail/container, this action can't actually reboot the host --
	// it restarts the Sylve process instead. Label it accordingly.
	let jailed = $state(false);

	onMount(async () => {
		const health = await probeBasicHealth();
		jailed = health?.jailed === true;
	});

	async function waitForRebootCycle({
		downIntervalMs = 200,
		downTimeoutMs = 30 * 1000,
		upIntervalMs = 1000,
		upTimeoutMs = 60 * 60 * 1000
	} = {}) {
		// The restart flag is persisted before the process actually exits
		// (see RebootSystem() in core.go), so polling for it alone can hit
		// the *old*, still-running process and report success before a
		// restart has actually happened. Require the server to actually go
		// unreachable first -- proving the old process died -- before
		// waiting for it to come back. The down-window for a jailed restart
		// is narrow (~500ms exit delay + ~1s app boot), so poll tightly here
		// or a slower interval can straddle the whole cycle and miss it.
		const downStart = Date.now();
		let wentDown = false;
		while (Date.now() - downStart < downTimeoutMs) {
			const health = await probeBasicHealth();
			if (health === null) {
				wentDown = true;
				break;
			}
			await new Promise((r) => setTimeout(r, downIntervalMs));
		}
		if (!wentDown) {
			throw new Error('Service did not go down for restart');
		}

		const upStart = Date.now();
		while (Date.now() - upStart < upTimeoutMs) {
			const health = await probeBasicHealth();
			if (health?.initialized === true && health.restarted === true) {
				return true;
			}

			await new Promise((r) => setTimeout(r, upIntervalMs));
		}

		throw new Error('Reboot cycle not completed in time');
	}

	async function handleReboot() {
		if (rebootInitiated) return;
		rebootInitiated = true;

		try {
			const response = await rebootSystem();
			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error('Failed to request a system reboot', { position: 'bottom-center' });
				rebootInitiated = false;
				return;
			}

			toast.info(jailed ? 'Sylve is restarting...' : 'System is restarting...', {
				position: 'bottom-center'
			});
			await waitForRebootCycle();
			toast.success(jailed ? 'Sylve is back online' : 'System is back online', {
				position: 'bottom-center'
			});
			setTimeout(() => {
				window.location.href = '/datacenter/summary';
			}, 1000);
		} catch {
			toast.error('System did not come back online in time. You can try again.', {
				position: 'bottom-center'
			});
			rebootInitiated = false;
		}
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
				{#if jailed}
					A <span class="font-medium text-foreground">quick restart</span> is required for Sylve to
					finish initialization. Continue when ready.
				{:else}
					A <span class="font-medium text-foreground">full system reboot</span> is required for Sylve
					to finish initialization. Continue when ready.
				{/if}
			</p>

			<Button onclick={handleReboot} class="px-8 py-2.5" disabled={rebootInitiated}>
				{#if rebootInitiated}
					<span class="icon icon-[mdi--loading] w-4 h-4 mr-2 inline-block animate-spin"></span>
					{jailed ? 'Restarting...' : 'Rebooting...'}
				{:else}
					<span class="icon icon-[mdi--restart] w-4 h-4 mr-2 inline-block"></span>
					{jailed ? 'Restart' : 'Reboot'}
				{/if}
			</Button>
		</div>
	</div>
</div>
