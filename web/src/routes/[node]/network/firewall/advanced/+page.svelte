<script lang="ts">
	import {
		getFirewallAdvancedSettings,
		updateFirewallAdvancedSettings
	} from '$lib/api/network/firewall';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { FirewallAdvancedSettings } from '$lib/types/network/firewall';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		advancedSettings: FirewallAdvancedSettings | APIResponse;
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	const advancedResource = resource(
		() => 'firewall-advanced-settings',
		async (key) => {
			const result = await getFirewallAdvancedSettings();
			updateCache(key, result);
			return result;
		},
		{
			initialValue:
				typeof data.advancedSettings === 'object' &&
				data.advancedSettings !== null &&
				'id' in data.advancedSettings
					? (data.advancedSettings as FirewallAdvancedSettings)
					: ({
							id: 0,
							preRules: '',
							postRules: '',
							createdAt: new Date().toISOString(),
							updatedAt: new Date().toISOString()
						} as FirewallAdvancedSettings)
		}
	);

	let form = $state({
		preRules: (advancedResource.current as FirewallAdvancedSettings).preRules ?? '',
		postRules: (advancedResource.current as FirewallAdvancedSettings).postRules ?? ''
	});

	$effect(() => {
		form.preRules = (advancedResource.current as FirewallAdvancedSettings).preRules ?? '';
		form.postRules = (advancedResource.current as FirewallAdvancedSettings).postRules ?? '';
	});

	async function saveAdvancedSettings() {
		const result = await updateFirewallAdvancedSettings(form.preRules ?? '', form.postRules ?? '');

		if (result.status === 'success') {
			toast.success('Advanced firewall settings updated', { position: 'bottom-center' });
			await advancedResource.refetch();
		} else {
			handleAPIError(result);
			toast.error('Failed to update advanced settings', { position: 'bottom-center' });
		}
	}
</script>

<div class="flex h-full w-full flex-col gap-4 p-4">
	<div class="rounded-lg border bg-card p-4">
		<div class="flex items-center gap-3">
			<div class="bg-primary/10 flex h-10 w-10 shrink-0 items-center justify-center rounded-md">
				<span class="icon-[mdi--shield-edit] text-primary h-5 w-5"></span>
			</div>
			<div>
				<h2 class="text-lg font-semibold">Advanced Firewall Settings</h2>
				<p class="text-muted-foreground text-sm">
					Global PF rules injected before and after Sylve anchors in
					<code class="bg-muted rounded px-1 py-0.5 font-mono text-xs">/etc/pf.conf</code>.
				</p>
			</div>
		</div>
		<div
			class="border-amber-500/30 bg-amber-500/5 text-amber-600 dark:text-amber-400 mt-4 flex items-start gap-2 rounded-md border px-3 py-2 text-sm"
		>
			<span class="icon-[mdi--alert-circle-outline] mt-0.5 h-4 w-4 shrink-0"></span>
			<span>Rules here are applied globally and may override managed rules. Use with caution.</span>
		</div>
	</div>

	<!-- Editors -->
	<div class="rounded-lg border bg-card p-4">
		<div class="grid grid-cols-1 gap-6 md:grid-cols-2">
			<div class="flex flex-col gap-1.5">
				<div class="flex items-center gap-1.5 text-sm font-medium">
					<span class="icon-[mdi--arrow-up-circle-outline] h-4 w-4 text-blue-500"></span>
					Pre Rules
				</div>
				<p class="text-muted-foreground text-xs">Injected before all Sylve-managed anchors.</p>
				<CustomValueInput
					label=""
					placeholder="# Rules placed before Sylve anchors"
					bind:value={form.preRules}
					classes="flex-1"
					type="textarea"
					textAreaClasses="min-h-56 font-mono text-xs resize-y"
				/>
			</div>
			<div class="flex flex-col gap-1.5">
				<div class="flex items-center gap-1.5 text-sm font-medium">
					<span class="icon-[mdi--arrow-down-circle-outline] h-4 w-4 text-green-500"></span>
					Post Rules
				</div>
				<p class="text-muted-foreground text-xs">Injected after all Sylve-managed anchors.</p>
				<CustomValueInput
					label=""
					placeholder="# Rules placed after Sylve anchors"
					bind:value={form.postRules}
					classes="flex-1"
					type="textarea"
					textAreaClasses="min-h-56 font-mono text-xs resize-y"
				/>
			</div>
		</div>

		<div class="mt-4 flex items-center justify-between border-t pt-4">
			<p class="text-muted-foreground flex items-center gap-1.5 text-xs">
				<span class="icon-[mdi--information-outline] h-3.5 w-3.5"></span>
				Changes take effect immediately upon saving.
			</p>
			<Button size="sm" onclick={saveAdvancedSettings} class="gap-1.5">
				<span class="icon-[mdi--content-save-outline] h-4 w-4"></span>
				Save Settings
			</Button>
		</div>
	</div>
</div>
