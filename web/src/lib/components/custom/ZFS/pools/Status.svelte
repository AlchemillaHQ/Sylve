<script lang="ts">
	import { getPoolStatus } from '$lib/api/zfs/pool';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Zpool, ZpoolStatusScanStats, ZpoolStatusVDEV } from '$lib/types/zfs/pool';
	import { parseScanStats } from '$lib/utils/zfs/pool';
	import { IsDocumentVisible, resource, useInterval } from 'runed';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';

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

	function num(v?: string | number | null): number {
		if (v === undefined || v === null) return 0;
		return typeof v === 'string' ? Number(v) : v;
	}

	function hasErrors(v: ZpoolStatusVDEV): boolean {
		return num(v.read_errors) > 0 || num(v.write_errors) > 0 || num(v.checksum_errors) > 0;
	}

	const scanErrors = $derived(Number(status.current?.scan_stats?.errors ?? 0));
	const hasVdevErrors = $derived.by(() => {
		const walk = (vdevs?: Record<string, ZpoolStatusVDEV> | null): boolean => {
			if (!vdevs) return false;
			return Object.values(vdevs).some(
				(v) =>
					num(v.read_errors) > 0 ||
					num(v.write_errors) > 0 ||
					num(v.checksum_errors) > 0 ||
					walk(v.vdevs)
			);
		};

		return walk(status.current?.vdevs);
	});

	const hasAnyErrors = $derived(scanErrors > 0 || hasVdevErrors);
</script>

{#snippet VdevTree(vdev: ZpoolStatusVDEV, scan?: ZpoolStatusScanStats, depth = 0)}
	<div class="relative mt-2 {depth > 0 ? 'ml-4' : ''}">
		<div class="flex items-center gap-2 rounded-md border p-2">
			<!-- state dot -->
			<span
				class="h-2.5 w-2.5 rounded-full"
				class:bg-green-500={vdev.state === 'ONLINE'}
				class:bg-yellow-500={vdev.state === 'DEGRADED'}
				class:bg-red-500={vdev.state === 'FAULTED'}
				class:bg-gray-400={!vdev.state || !['ONLINE', 'DEGRADED', 'FAULTED'].includes(vdev.state)}
			/>

			<span class="font-medium">
				{vdev.name ?? '(unknown)'}
			</span>

			{#if hasErrors(vdev)}
				<div class="ml-auto flex gap-2 text-xs text-red-600">
					{#if num(vdev.read_errors) > 0}
						<span>R:{num(vdev.read_errors)}</span>
					{/if}
					{#if num(vdev.write_errors) > 0}
						<span>W:{num(vdev.write_errors)}</span>
					{/if}
					{#if num(vdev.checksum_errors) > 0}
						<span>C:{num(vdev.checksum_errors)}</span>
					{/if}
				</div>
			{/if}
		</div>

		<!-- children -->
		{#if vdev.vdevs}
			<div class="ml-2 border-l pl-2">
				{#each Object.values(vdev.vdevs) as child (child.guid ?? child.name)}
					{@render VdevTree(child, scan, depth + 1)}
				{/each}
			</div>
		{/if}
	</div>
{/snippet}

{#if pool !== null && status !== undefined && status.current !== undefined}
	<Dialog.Root bind:open>
		<Dialog.Content class="sm:w-full md:min-w-3xl">
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

				<!-- Device Topology -->
				<div class="border">
					<div class="bg-muted flex items-center gap-2 px-4 py-2">
						<span class="icon-[tabler--topology-bus] text-primary h-5 w-5"></span>
						<span class="font-semibold">Device Topology</span>
					</div>

					<ScrollArea orientation="vertical" class="h-64 w-full">
						<div class="px-3 py-2 space-y-3">
							{#if status.current?.vdevs}
								{#each Object.values(status.current.vdevs) as root (root.guid ?? root.name)}
									{@render VdevTree(root, status.current.scan_stats || undefined)}
								{/each}
							{:else}
								<div class="text-muted-foreground flex items-center gap-2 py-2">
									<span class="icon-[material-symbols--info] h-4 w-4"></span>
									<span>No devices found</span>
								</div>
							{/if}

							{#if status.current?.logs}
								<div class="border mt-4">
									<div class="bg-muted gap-2 px-4 py-2 font-semibold flex items-center">
										<span class="icon icon-[octicon--log-16] h-4 w-4 align-middle"></span>
										<span>Logs</span>
									</div>
									<div class="p-4 space-y-2">
										{#each Object.values(status.current.logs) as log (log.guid ?? log.name)}
											{@render VdevTree(log)}
										{/each}
									</div>
								</div>
							{/if}

							{#if status.current?.spares}
								<div class="border mt-4">
									<div class="bg-muted gap-2 px-4 py-2 font-semibold flex items-center">
										<span class="icon icon-[bi--hdd-stack-fill] h-4 w-4 align-middle"></span>
										<span>Spares</span>
									</div>
									<div class="p-4 space-y-2">
										{#each Object.values(status.current.spares) as spare (spare.guid ?? spare.name)}
											{@render VdevTree(spare)}
										{/each}
									</div>
								</div>
							{/if}

							{#if status.current?.l2cache}
								<div class="border mt-4">
									<div class="bg-muted gap-2 px-4 py-2 font-semibold flex items-center">
										<span class="icon icon-[octicon--cache-16] h-4 w-4 align-middle"></span>
										<span>Cache</span>
									</div>
									<div class="p-4 space-y-2">
										{#each Object.values(status.current.l2cache) as cache (cache.guid ?? cache.name)}
											{@render VdevTree(cache)}
										{/each}
									</div>
								</div>
							{/if}
						</div>
					</ScrollArea>
				</div>

				<!-- Error Status -->
				<div class="border">
					<div class="bg-muted flex items-center gap-2 px-4 py-2">
						<span class="icon-[mdi--alert] text-primary h-5 w-5"></span>
						<span class="font-semibold">Error Status</span>
					</div>

					<div class="p-4">
						<div
							class="flex items-center gap-2 rounded-md border p-2
				{!hasAnyErrors
								? 'border-green-200 bg-green-50 text-green-800 dark:border-green-800 dark:bg-green-950 dark:text-green-200'
								: 'border-red-200 bg-red-50 text-red-800 dark:border-red-800 dark:bg-red-950 dark:text-red-200'}"
						>
							<span class="icon-[mdi--check-circle] h-5 w-5" class:hidden={hasAnyErrors} />
							<span class="icon-[mdi--alert-circle] h-5 w-5" class:hidden={!hasAnyErrors} />

							<span>
								{!hasAnyErrors
									? 'No known data errors'
									: `Errors detected${scanErrors > 0 ? ` during scan (${scanErrors})` : ''}`}
							</span>
						</div>
					</div>
				</div>
			</div>
		</Dialog.Content>
	</Dialog.Root>
{/if}
