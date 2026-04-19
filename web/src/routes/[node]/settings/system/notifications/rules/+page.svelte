<script lang="ts">
	import { getNotificationRules, updateNotificationRules } from '$lib/api/notifications';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { NotificationRulesConfig } from '$lib/types/notifications';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		rules: NotificationRulesConfig;
	}

	let { data }: { data: Data } = $props();

	let rulesResource = resource(
		() => 'notification-rules',
		async (key) => {
			const loaded = await getNotificationRules();
			if (!isAPIResponse(loaded)) {
				updateCache(key, loaded);
			}
			return isAPIResponse(loaded) ? data.rules : loaded;
		},
		{ initialValue: data.rules }
	);

	let editableRules = $state<NotificationRulesConfig['rules']>([]);
	let saving = $state(false);

	$effect(() => {
		editableRules = ((rulesResource.current as NotificationRulesConfig).rules || []).map((rule) => ({
			...rule
		}));
	});

	let dirty = $derived.by(() => {
		return (
			JSON.stringify(editableRules) !==
			JSON.stringify((rulesResource.current as NotificationRulesConfig).rules || [])
		);
	});

	async function saveRules() {
		if (!dirty || saving) {
			return;
		}

		saving = true;
		const response = await updateNotificationRules({ rules: editableRules });
		saving = false;

		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to update notification rules', { position: 'bottom-center' });
			return;
		}

		toast.success('Notification rules updated', { position: 'bottom-center' });
		rulesResource.refetch();
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<span class="text-muted-foreground text-xs sm:text-sm">Per-pool ZFS state-change notification rules</span>
		<div class="ml-auto flex items-center gap-2">
			<Button size="sm" variant="outline" class="h-6.5" onclick={() => rulesResource.refetch()}>
				Refresh
			</Button>
			<Button size="sm" class="h-6.5" onclick={saveRules} disabled={!dirty || saving}>
				{#if saving}
					<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>
				{/if}
				Save
			</Button>
		</div>
	</div>

	<div class="h-full overflow-auto">
		<table class="w-full text-sm">
			<thead class="bg-muted/50 sticky top-0">
				<tr class="border-b text-left">
					<th class="p-2 font-medium">Pool</th>
					<th class="p-2 font-medium">In-App</th>
					<th class="p-2 font-medium">ntfy</th>
					<th class="p-2 font-medium">Email</th>
				</tr>
			</thead>
			<tbody>
				{#if editableRules.length === 0}
					<tr>
						<td colspan={4} class="text-muted-foreground p-4 text-center">
							No active ZFS pools found.
						</td>
					</tr>
				{:else}
					{#each editableRules as rule (rule.kind)}
						<tr class="border-b">
							<td class="p-2 font-medium">{rule.pool}</td>
							<td class="p-2">
								<input type="checkbox" bind:checked={rule.uiEnabled} class="h-4 w-4" />
							</td>
							<td class="p-2">
								<input type="checkbox" bind:checked={rule.ntfyEnabled} class="h-4 w-4" />
							</td>
							<td class="p-2">
								<input type="checkbox" bind:checked={rule.emailEnabled} class="h-4 w-4" />
							</td>
						</tr>
					{/each}
				{/if}
			</tbody>
		</table>
	</div>
</div>
