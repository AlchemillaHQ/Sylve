<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import { getZFSDashboardHistory, getZFSDashboardSnapshot } from '$lib/api/zfs/pool';
	import ARCPanel from '$lib/components/custom/ZFS/Dashboard/ARCPanel.svelte';
	import OperationalStat from '$lib/components/custom/ZFS/Dashboard/OperationalStat.svelte';
	import PerformanceChart, {
		type PerformanceMetric
	} from '$lib/components/custom/ZFS/Dashboard/PerformanceChart.svelte';
	import PoolStatusList from '$lib/components/custom/ZFS/Dashboard/PoolStatusList.svelte';
	import {
		aggregatePoolHistory,
		capacityTone,
		compactNumber,
		healthRank,
		historyWindowLabel,
		ranges,
		statusTone,
		summarizeIO,
		summarizePools,
		summarizeVerification,
		type RangeKey
	} from '$lib/components/custom/ZFS/Dashboard/dashboard';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import type { ZFSDashboardHistory, ZFSDashboardSnapshot } from '$lib/types/zfs/pool';
	import { formatBytesBinary, formatBytesPerSecondBinary } from '$lib/utils/bytes';
	import { updateCache } from '$lib/utils/http';
	import { resource, useInterval, watch } from 'runed';

	const ALL_POOLS = '__all__';
	const MAX_CHART_POINTS = 900;

	interface Data {
		hostname: string;
		snapshot: ZFSDashboardSnapshot;
		history: ZFSDashboardHistory;
	}

	let { data }: { data: Data } = $props();
	let selectedPool = $state(ALL_POOLS);
	let selectedRange = $state<RangeKey>('24h');
	let performanceMetric = $state<PerformanceMetric>('bandwidth');
	let refreshing = $state(false);

	// svelte-ignore state_referenced_locally
	const snapshot = resource(
		() => `zfs-dashboard-snapshot:${data.hostname}`,
		async (key, _previous, { signal }) => {
			const result = await getZFSDashboardSnapshot({ hostname: data.hostname, signal });
			void updateCache(key, result);
			return result;
		},
		{ initialValue: data.snapshot }
	);

	// svelte-ignore state_referenced_locally
	const history = resource(
		[() => data.hostname, () => selectedRange, () => selectedPool],
		async ([hostname, range, poolGUID], _previous, { signal }) => {
			const rangeSeconds = ranges.find((option) => option.value === range)?.seconds ?? 24 * 60 * 60;
			const scopedGUID = poolGUID === ALL_POOLS ? '' : poolGUID;
			const key = `zfs-dashboard-history:${hostname}:${rangeSeconds}:${scopedGUID || 'all'}`;
			const result = await getZFSDashboardHistory(rangeSeconds, scopedGUID, MAX_CHART_POINTS, {
				hostname,
				signal
			});
			void updateCache(key, result);
			return result;
		},
		{ initialValue: data.history }
	);

	let poolOptions = $derived([
		{ value: ALL_POOLS, label: 'All pools' },
		...snapshot.current.pools.map((pool) => ({ value: pool.guid, label: pool.name }))
	]);
	let focusedPools = $derived(
		selectedPool === ALL_POOLS
			? snapshot.current.pools
			: snapshot.current.pools.filter((pool) => pool.guid === selectedPool)
	);
	let summary = $derived(summarizePools(focusedPools));
	let ioSummary = $derived(summarizeIO(focusedPools));
	let verification = $derived(summarizeVerification(focusedPools));
	let selectedHistory = $derived(
		selectedPool === ALL_POOLS
			? aggregatePoolHistory(history.current.pools)
			: (history.current.pools.find((series) => series.guid === selectedPool)?.points ?? [])
	);
	let selectedScopeLabel = $derived(
		selectedPool === ALL_POOLS
			? 'All pools'
			: (snapshot.current.pools.find((pool) => pool.guid === selectedPool)?.name ?? 'Selected pool')
	);
	let totalThroughput = $derived(ioSummary.readBytesPerSecond + ioSummary.writeBytesPerSecond);
	let totalIOPS = $derived(ioSummary.readIOPS + ioSummary.writeIOPS);
	let healthTone = $derived(statusTone(summary.health, summary.errors, summary.statusUnavailable));
	let hasAttention = $derived(
		focusedPools.length > 0 &&
			(snapshot.current.stale ||
				healthRank(summary.health) > 1 ||
				summary.errors > 0 ||
				summary.statusUnavailable > 0)
	);
	let attentionText = $derived.by(() => {
		if (snapshot.current.stale)
			return 'Live ZFS telemetry is stale. Historical data remains available.';
		if (summary.errors > 0)
			return `${summary.errors} ZFS error${summary.errors === 1 ? '' : 's'} require review in the selected scope.`;
		if (healthRank(summary.health) > 1)
			return `Pool state is ${summary.health}. Review pool and device status.`;
		return `Detailed device status is unavailable for ${summary.statusUnavailable} pool${summary.statusUnavailable === 1 ? '' : 's'}.`;
	});
	let sampledLabel = $derived(
		snapshot.current.sampledAt > 0
			? `Sampled ${new Date(snapshot.current.sampledAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}`
			: 'Waiting for samples'
	);

	async function refreshDashboard() {
		if (refreshing) return;
		refreshing = true;
		try {
			await Promise.all([snapshot.refetch(), history.refetch()]);
		} finally {
			refreshing = false;
		}
	}

	useInterval(10_000, {
		callback: () => {
			if (storage.visible && !storage.idle) void snapshot.refetch();
		}
	});

	useInterval(60_000, {
		callback: () => {
			if (storage.visible && !storage.idle) void history.refetch();
		}
	});

	watch(
		[() => data.hostname, () => snapshot.current.pools.map((pool) => pool.guid).join('|')],
		([hostname, poolGUIDs], [previousHostname]) => {
			if (
				hostname !== previousHostname ||
				(selectedPool !== ALL_POOLS && !poolGUIDs.split('|').includes(selectedPool))
			) {
				selectedPool = ALL_POOLS;
			}
		},
		{ lazy: true }
	);

	watch(
		() => storage.visible,
		(visible, previousVisible) => {
			if (visible && !previousVisible) void refreshDashboard();
		},
		{ lazy: true }
	);
</script>

<svelte:head>
	<title>ZFS Dashboard · {data.hostname}</title>
</svelte:head>

<div class="dashboard-root">
	<header class="dashboard-toolbar">
		<div class="flex min-w-0 items-center gap-3">
			<span class="icon-[file-icons--openzfs] size-5 shrink-0 text-blue-500" aria-hidden="true"
			></span>
			<div class="min-w-0">
				<div class="flex flex-wrap items-center gap-2">
					<h1 class="text-lg font-semibold tracking-tight">ZFS</h1>
					<Badge
						variant="outline"
						class={snapshot.current.stale
							? 'border-amber-500/30 text-amber-600 dark:text-amber-400'
							: 'text-muted-foreground'}
					>
						{snapshot.current.stale ? 'Stale' : sampledLabel}
					</Badge>
				</div>
				<p class="text-muted-foreground truncate text-xs">
					Operational storage and performance on {data.hostname}
				</p>
			</div>
		</div>

		<div class="flex min-w-0 items-center gap-2">
			<SimpleSelect
				label="Pool scope"
				icon="icon-[mdi--database-outline]"
				options={poolOptions}
				bind:value={selectedPool}
				onChange={(value) => (selectedPool = value)}
				size="sm"
				classes={{
					parent: 'w-auto',
					label: 'sr-only',
					trigger: 'w-fit max-w-48 min-w-0 gap-1.5 px-2.5'
				}}
				title="Pool scope"
			/>
			<Button
				variant="outline"
				size="icon"
				class="size-8 shrink-0"
				onclick={refreshDashboard}
				disabled={refreshing}
				aria-label="Refresh dashboard"
				title="Refresh dashboard"
			>
				<span
					class={['icon-[mdi--refresh] size-4', refreshing && 'animate-spin']}
					aria-hidden="true"
				></span>
			</Button>
		</div>
	</header>

	<main class="space-y-4 p-4">
		<section class="summary-grid" aria-label="ZFS operational summary">
			<OperationalStat
				label="Storage health"
				value={focusedPools.length > 0
					? `${summary.online} / ${focusedPools.length} online`
					: 'No pools'}
				detail={summary.errors > 0
					? `${summary.errors} known errors`
					: summary.statusUnavailable > 0
						? 'Some device status unavailable'
						: 'No known pool or device errors'}
				footer={selectedScopeLabel}
				iconClass={healthTone === 'success'
					? 'icon-[mdi--shield-check-outline]'
					: 'icon-[mdi--shield-alert-outline]'}
				tone={healthTone}
			/>

			<OperationalStat
				label="Capacity"
				value={`${summary.usedPercent.toFixed(1)}% used`}
				detail={`${formatBytesBinary(summary.allocated)} of ${formatBytesBinary(summary.totalSize)}`}
				footer={`${formatBytesBinary(summary.free)} free`}
				iconClass="icon-[mdi--database-outline]"
				tone={capacityTone(summary.usedPercent)}
				progress={summary.usedPercent}
			/>

			<OperationalStat
				label="Pool verification"
				value={verification.value}
				detail={verification.detail}
				footer={verification.footer}
				iconClass="icon-[lets-icons--check-fill]"
				tone={verification.tone}
				progress={verification.progress}
			/>

			<OperationalStat
				label="Live I/O"
				value={ioSummary.valid ? formatBytesPerSecondBinary(totalThroughput) : 'Unavailable'}
				detail={`Read ${formatBytesPerSecondBinary(ioSummary.readBytesPerSecond)} · Write ${formatBytesPerSecondBinary(ioSummary.writeBytesPerSecond)}`}
				footer={`${compactNumber(totalIOPS)} IOPS · ${ioSummary.averageLatency === null ? 'latency unavailable' : `${ioSummary.averageLatency.toFixed(1)} ms average`} · ${ioSummary.intervalSeconds}s window`}
				iconClass="icon-[mdi--swap-vertical-bold]"
				tone={snapshot.current.stale ? 'warning' : 'neutral'}
			/>
		</section>

		{#if hasAttention}
			<div
				class={[
					'flex items-center gap-2 rounded-md border px-3 py-2 text-sm',
					summary.errors > 0 || healthRank(summary.health) >= 4
						? 'border-red-500/30 bg-red-500/5 text-red-700 dark:text-red-300'
						: 'border-amber-500/30 bg-amber-500/5 text-amber-700 dark:text-amber-300'
				]}
				role="alert"
			>
				<span class="icon-[mdi--alert-circle-outline] size-4 shrink-0" aria-hidden="true"></span>
				<span>{attentionText}</span>
			</div>
		{/if}

		<section class="primary-grid" aria-label="Pool status and performance">
			<Card.Root class="gap-0 py-0 shadow-none">
				<Card.Header class="p-4 pb-3">
					<div class="flex items-start justify-between gap-3">
						<div>
							<h2 class="flex items-center gap-2 text-sm font-semibold">
								<span
									class="icon-[mdi--database-check-outline] size-4 text-blue-500"
									aria-hidden="true"
								></span>
								Pools
							</h2>
							<p class="text-muted-foreground mt-1 text-xs">Capacity, integrity, and scan state.</p>
						</div>
						<Badge variant="secondary">{snapshot.current.pools.length}</Badge>
					</div>
				</Card.Header>

				<Card.Content class="px-0 pb-0">
					{#if snapshot.current.pools.length > 0}
						<PoolStatusList
							pools={snapshot.current.pools}
							selectedGUID={selectedPool}
							onSelect={(guid) => (selectedPool = guid)}
						/>
						<div class="detail-grid p-4">
							<div>
								<p class="text-muted-foreground text-xs">Topology</p>
								<p class="mt-1 text-sm font-medium tabular-nums">
									{summary.dataVdevs} VDEV{summary.dataVdevs === 1 ? '' : 's'} · {summary.disks} disk{summary.disks ===
									1
										? ''
										: 's'}
								</p>
							</div>
							<div>
								<p class="text-muted-foreground text-xs">Fragmentation</p>
								<p class="mt-1 text-sm font-medium tabular-nums">
									{summary.fragmentation.toFixed(0)}%
								</p>
							</div>
							<div>
								<p class="text-muted-foreground text-xs">Dedup ratio</p>
								<p class="mt-1 text-sm font-medium tabular-nums">
									{summary.dedupRatio.toFixed(2)}×
								</p>
							</div>
						</div>
					{:else}
						<div
							class="text-muted-foreground flex min-h-64 flex-col items-center justify-center gap-2 p-4 text-center"
							role="status"
						>
							<span class="icon-[bi--hdd-stack] size-7" aria-hidden="true"></span>
							<p class="text-sm font-medium">No imported ZFS pools found</p>
						</div>
					{/if}
				</Card.Content>
			</Card.Root>

			<Card.Root class="gap-0 py-0 shadow-none">
				<Card.Header class="p-4 pb-2">
					<div class="performance-header">
						<div>
							<h2 class="flex items-center gap-2 text-sm font-semibold">
								<span
									class="icon-[mdi--chart-timeline-variant] size-4 text-blue-500"
									aria-hidden="true"
								></span>
								Performance
							</h2>
							<p class="text-muted-foreground mt-1 text-xs">
								Historical logical I/O for {selectedScopeLabel.toLowerCase()}.
							</p>
						</div>
						<div class="chart-controls">
							<div class="segmented" role="group" aria-label="Performance metric">
								{#each [{ value: 'bandwidth', label: 'Bandwidth' }, { value: 'operations', label: 'IOPS' }, { value: 'latency', label: 'Latency' }] as metric (metric.value)}
									<button
										type="button"
										class:active={performanceMetric === metric.value}
										onclick={() => (performanceMetric = metric.value as PerformanceMetric)}
										aria-pressed={performanceMetric === metric.value}>{metric.label}</button
									>
								{/each}
							</div>
							<div class="segmented" role="group" aria-label="History range">
								{#each ranges as range (range.value)}
									<button
										type="button"
										class:active={selectedRange === range.value}
										onclick={() => (selectedRange = range.value)}
										aria-pressed={selectedRange === range.value}>{range.label}</button
									>
								{/each}
							</div>
						</div>
					</div>
				</Card.Header>
				<Card.Content class="px-2 pt-0 pb-3">
					<PerformanceChart points={selectedHistory} metric={performanceMetric} />
					<p class="text-muted-foreground border-t px-1 pt-3 text-xs">
						{historyWindowLabel(selectedHistory, history.current.resolutionSeconds)}
					</p>
				</Card.Content>
			</Card.Root>
		</section>

		<Card.Root class="gap-0 py-0 shadow-none">
			<Card.Header class="p-4 pb-2">
				<h2 class="flex items-center gap-2 text-sm font-semibold">
					<span class="icon-[mdi--memory] size-4 text-blue-500" aria-hidden="true"></span>
					Node ARC
				</h2>
				<p class="text-muted-foreground mt-1 text-xs">
					Node-wide cache efficiency, sizing, composition, and pressure.
				</p>
			</Card.Header>
			<Card.Content class="px-4 pt-2 pb-4">
				<ARCPanel arc={snapshot.current.arc} />
			</Card.Content>
		</Card.Root>
	</main>
</div>

<style>
	.dashboard-root {
		container-type: inline-size;
	}

	.dashboard-toolbar {
		position: sticky;
		top: 0;
		z-index: 20;
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		border-bottom: 1px solid var(--border);
		background: color-mix(in oklab, var(--background) 94%, transparent);
		padding: 0.75rem 1rem;
		backdrop-filter: blur(10px);
	}

	.summary-grid,
	.primary-grid,
	.detail-grid {
		display: grid;
		gap: 1rem;
	}

	.summary-grid,
	.primary-grid {
		grid-template-columns: minmax(0, 1fr);
	}

	.primary-grid {
		align-items: start;
	}

	.detail-grid {
		grid-template-columns: repeat(auto-fit, minmax(100px, 1fr));
	}

	.performance-header,
	.chart-controls {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		justify-content: space-between;
		gap: 0.75rem;
	}

	.segmented {
		display: inline-flex;
		align-items: center;
		border-radius: calc(var(--radius) + 2px);
		background: var(--muted);
		padding: 2px;
	}

	.segmented button {
		border-radius: var(--radius);
		padding: 0.3rem 0.55rem;
		color: var(--muted-foreground);
		font-size: 0.75rem;
		line-height: 1rem;
		transition:
			background-color 150ms,
			color 150ms;
	}

	.segmented button:hover {
		color: var(--foreground);
	}

	.segmented button:focus-visible {
		outline: 2px solid var(--ring);
		outline-offset: 1px;
	}

	.segmented button.active {
		background: var(--background);
		color: var(--foreground);
		box-shadow: 0 1px 2px rgb(0 0 0 / 0.08);
	}

	@container (min-width: 620px) {
		.summary-grid {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
	}

	@container (min-width: 900px) {
		.summary-grid {
			grid-template-columns: repeat(4, minmax(0, 1fr));
		}
	}
</style>
