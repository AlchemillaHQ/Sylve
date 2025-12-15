<script lang="ts">
	import { getPoolStatus } from '$lib/api/zfs/pool';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { capitalizeFirstLetter } from '$lib/utils/string';
	import { parseScanStats } from '$lib/utils/zfs/pool';
	import { IsDocumentVisible, resource, useInterval } from 'runed';
	import { onMount } from 'svelte';

	interface Props {
		open: boolean;
		pool: Zpool;
	}

	let visible = new IsDocumentVisible();
	let { open = $bindable(), pool }: Props = $props();

	const guid = $state.raw(pool?.guid || '');
	let status = resource(
		() => `pool-status-${guid}`,
		async () => await getPoolStatus(guid)
	);

	let stateArr = $derived.by(() => {
		const s = pool?.state || 'UNKNOWN';
		if (!pool) return 'bg-gray-500 text-white';
		switch (s) {
			case 'ONLINE':
				return ['ONLINE', 'bg-green-500 text-white'];
			case 'DEGRADED':
				return ['DEGRADED', 'bg-yellow-500 text-white'];
			case 'FAULTED':
				return ['FAULTED', 'bg-red-500 text-white'];
			case 'UNAVAIL':
				return ['UNAVAIL', 'bg-gray-700 text-white'];
			case 'OFFLINE':
				return ['OFFLINE', 'bg-gray-600 text-white'];
			case 'REMOVED':
				return ['REMOVED', 'bg-red-700 text-white'];
			case 'SUSPENDED':
				return ['SUSPENDED', 'bg-purple-600 text-white'];
			default:
				return ['UNKNOWN', 'bg-gray-500 text-white'];
		}
	});

	let scanActivity = $derived.by(() => {
		if (status.current?.scan_stats) {
			return parseScanStats(status.current.scan_stats);
		}

		return null;
	});

	useInterval(1000, {
		callback: async () => {
			if (open && visible.current) {
				status.refetch();
			}
		}
	});

	$inspect(scanActivity);
	// $inspect(status.current);
</script>

{#if pool !== null && status !== undefined && status.current !== undefined}
	<Dialog.Root bind:open>
		<Dialog.Content class="min-w-2xl">
			<div class="flex items-center justify-between pb-3">
				<Dialog.Header>
					<Dialog.Title class="flex items-center">
						<span class="text-primary font-semibold">Pool Status</span>
						<span class="text-muted-foreground mx-2">•</span>
						<span class="text-xl font-medium">{pool.name}</span>
						<Badge class="mt-0.5 ml-2 {stateArr[1]} font-bold">{stateArr[0]}</Badge>
					</Dialog.Title>
				</Dialog.Header>

				<Dialog.Close
					class="flex h-5 w-5 cursor-pointer items-center justify-center rounded-sm opacity-70 transition-opacity hover:opacity-100"
				>
					<span class="icon-[material-symbols--close-rounded] h-5 w-5"></span>
				</Dialog.Close>
			</div>

			<div class="space-y-5">
				<!-- Warning -->
				{#if status.current.status && status.current.status.length > 0}
					<div
						class="rounded-md border border-yellow-200 bg-yellow-50 p-4 text-yellow-800 dark:border-yellow-800 dark:bg-yellow-950 dark:text-yellow-200"
					>
						<div class="flex gap-3">
							<span class="icon-[mdi--alert-circle] mt-0.5 h-5 w-5 shrink-0"></span>
							<div>
								<p class="font-medium">{status.current.status}</p>
								{#if status.current.action && status.current.action.length > 0}
									<p class="mt-2 text-sm">{status.current.action}</p>
								{/if}
							</div>
						</div>
					</div>
				{/if}

				<!-- Scan / Scrub activity -->
				{#if scanActivity !== null && scanActivity.title !== ''}
					<div class="space-y-4 overflow-hidden rounded-md">
						<div class="border">
							<div class="bg-muted flex items-center gap-2 px-4 py-2">
								<span class="icon-[mdi--magnify] text-primary h-5 w-5"></span>
								<span class="font-semibold">Scan Activity</span>
							</div>

							<div class="p-4">
								<div class="space-y-2 text-muted-foreground text-sm">
									{scanActivity.text}
								</div>

								{#if scanActivity.progressPercent !== null && scanActivity.progressPercent >= 0 && scanActivity.progressPercent != 100}
									<div class="mt-3">
										<div class="bg-secondary h-2.5 w-full overflow-hidden rounded-full">
											<div
												class="h-full rounded-full bg-blue-500"
												style="width: {scanActivity.progressPercent}%"
												role="progressbar"
												aria-valuemin="0"
												aria-valuemax="100"
												aria-valuenow={scanActivity.progressPercent}
											/>
										</div>
									</div>
								{/if}
							</div>
						</div>
					</div>
				{:else}
					<div class="text-muted-foreground flex items-center gap-2 py-1">
						<icon class="icon-[material-symbols--info] h-4 w-4"></icon>
						<span>No recent scan activity</span>
					</div>
				{/if}
			</div>
		</Dialog.Content>
	</Dialog.Root>
{/if}
