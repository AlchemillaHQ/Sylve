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
	import {
		capacityTone,
		ranges,
		statusTone,
		summarizePools,
		type RangeKey
	} from '$lib/components/custom/ZFS/Dashboard/dashboard';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import { ZFSDashboardHistorySchema } from '$lib/types/zfs/pool';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { cachedFetch, updateCache } from '$lib/utils/http';
	import { resource, useInterval, watch } from 'runed';
	import { onDestroy } from 'svelte';
	import Panel from '../Dashboard/Panel.svelte';
	import Cache from './Cache.svelte';
	import {
		toneMeter,
		toneText,
		usedPercent,
		verificationSummary,
		type DashboardData
	} from './dashboard';
	import Performance from './Performance.svelte';
	import Pools from './Pools.svelte';

	let { data }: { data: DashboardData } = $props();
	// svelte-ignore state_referenced_locally
	const initial = data;
	const lifetime = new AbortController();
	let selectedPool = $state('');
	let selectedRange = $state<RangeKey>('1h');
	let rangeSeconds = $derived(ranges.find((range) => range.value === selectedRange)!.seconds);
	let rangePending = $state(false);
	let rangeTimer: ReturnType<typeof setTimeout> | undefined;

	const snapshot = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const result = await getZFSDashboardSnapshot({ hostname, signal: requestSignal });
			requestSignal.throwIfAborted();
			void updateCache(`zfs-dashboard-snapshot:${hostname}`, result, hostname);
			return result;
		},
		{ initialValue: initial.snapshot, lazy: true }
	);

	const history = resource(
		() => rangeSeconds,
		async (seconds, _previous, { signal, refetching }) => {
			const hostname = initial.hostname;
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const key = `zfs-dashboard-history:${hostname}:${seconds}:all`;
			const fetchHistory = () =>
				getZFSDashboardHistory(seconds, '', 900, { hostname, signal: requestSignal });
			const result = refetching
				? await fetchHistory()
				: await cachedFetch(
						key,
						fetchHistory,
						5 * 60 * 1000,
						false,
						hostname,
						ZFSDashboardHistorySchema
					);
			requestSignal.throwIfAborted();
			if (refetching) void updateCache(key, result, hostname);
			return { seconds, data: result };
		},
		{ initialValue: { seconds: 3600, data: initial.history }, lazy: true }
	);

	watch(
		() => rangeSeconds,
		() => {
			clearTimeout(rangeTimer);
			rangePending = true;
			rangeTimer = setTimeout(() => (rangePending = false), 1000);
		},
		{ lazy: true }
	);

	let pools = $derived(
		selectedPool
			? snapshot.current.pools.filter((pool) => pool.guid === selectedPool)
			: snapshot.current.pools
	);
	let scopeLabel = $derived(selectedPool ? (pools[0]?.name ?? 'Selected Pool') : 'All Pools');
	let summary = $derived(summarizePools(pools));
	let verification = $derived(verificationSummary(pools));
	let health = $derived(statusTone(summary.health, summary.errors, summary.statusUnavailable));
	let capacity = $derived(capacityTone(summary.usedPercent));
	let fullPools = $derived(pools.filter((pool) => usedPercent(pool) >= 80));
	let busy = $derived(snapshot.loading || history.loading || rangePending);
	let historyMatches = $derived(!rangePending && history.current.seconds === rangeSeconds);
	let attention = $derived.by(() => {
		if (snapshot.error)
			return 'Storage data could not be refreshed. Showing the last successful sample.';
		if (snapshot.current.stale)
			return 'Storage telemetry is stale. These values are from the last available sample.';
		if (pools.length === 0) return '';
		if (summary.errors > 0)
			return `${summary.errors} pool or device error${summary.errors === 1 ? '' : 's'} reported. Review the pool details below.`;
		if (summary.online < pools.length)
			return 'One or more pools are not online. Review the pool status below.';
		if (summary.statusUnavailable > 0)
			return 'Detailed device status is unavailable for one or more pools.';
		if (fullPools.length > 0)
			return `${fullPools.length === 1 ? `Pool ${fullPools[0].name} is` : `${fullPools.length} pools are`} at least 80% full. Plan for more free space.`;
		return '';
	});

	async function refreshDashboard() {
		await Promise.all([
			!snapshot.loading && snapshot.refetch(),
			!rangePending && !history.loading && history.refetch()
		]);
	}

	const snapshotInterval = useInterval(10_000, {
		callback: () => {
			if (storage.visible && !storage.idle && !snapshot.loading) void snapshot.refetch();
		}
	});
	const historyInterval = useInterval(60_000, {
		callback: () => {
			if (storage.visible && !storage.idle && !rangePending && !history.loading)
				void history.refetch();
		}
	});
	onDestroy(() => {
		lifetime.abort();
		clearTimeout(rangeTimer);
		snapshotInterval.pause();
		historyInterval.pause();
	});
	watch(
		[() => storage.visible, () => storage.idle],
		([visible, idle]) => {
			if (visible && !idle) void refreshDashboard();
		},
		{ lazy: true }
	);
	watch(
		() => snapshot.current.pools,
		(available) => {
			if (selectedPool && !available.some((pool) => pool.guid === selectedPool)) selectedPool = '';
		},
		{ lazy: true }
	);
</script>

<svelte:head>
	<title>Storage Dashboard · {initial.hostname}</title>
</svelte:head>

{#snippet rangeLabel(key: RangeKey)}
	{#if key === '1h'}Last Hour{:else if key === '24h'}Last 24 Hours{:else if key === '7d'}Last 7 Days{:else if key === '30d'}Last
		30 Days{:else}Last 70 Days{/if}
{/snippet}

<div class="@container px-4 pt-3 pb-4 md:px-5 md:pb-5">
	<header class="flex flex-wrap items-center justify-between gap-3">
		<div class="flex min-w-0 items-center gap-3">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
				aria-hidden="true"
			>
				<span class="icon-[mdi--monitor-dashboard] size-5"></span>
			</span>
			<Breadcrumb.Root class="min-w-0">
				<Breadcrumb.List class="flex-nowrap items-baseline gap-2 sm:gap-2">
					<Breadcrumb.Item>
						<span class="text-lg font-medium tracking-tight sm:text-xl">Storage</span>
					</Breadcrumb.Item>
					<Breadcrumb.Separator class="text-muted-foreground/60 self-center" />
					<Breadcrumb.Item class="min-w-0">
						<h1 class="truncate text-lg leading-7 tracking-tight sm:text-xl">
							<Breadcrumb.Page class="font-semibold">Dashboard</Breadcrumb.Page>
						</h1>
					</Breadcrumb.Item>
				</Breadcrumb.List>
			</Breadcrumb.Root>
		</div>
		<div class="flex min-w-0 flex-wrap items-center gap-2">
			<DropdownMenu.Root>
				<DropdownMenu.Trigger>
					{#snippet child({ props })}
						<Button
							variant="outline"
							size="sm"
							class="h-8 min-w-0 gap-2 font-normal"
							aria-label={`Pool filter: ${scopeLabel}`}
							{...props}
						>
							<span
								class="icon-[bi--hdd-stack-fill] text-muted-foreground size-4 shrink-0"
								aria-hidden="true"
							></span>
							<span class="max-w-40 truncate">{scopeLabel}</span>
							<span
								class="icon-[lucide--chevron-down] text-muted-foreground size-4 shrink-0"
								aria-hidden="true"
							></span>
						</Button>
					{/snippet}
				</DropdownMenu.Trigger>
				<DropdownMenu.Content align="end" class="max-h-72 w-56 overflow-y-auto">
					<DropdownMenu.Label>Pool Filter</DropdownMenu.Label>
					<DropdownMenu.RadioGroup bind:value={selectedPool}>
						<DropdownMenu.RadioItem value="" class="cursor-pointer"
							>All Pools</DropdownMenu.RadioItem
						>
						{#each snapshot.current.pools as pool (pool.guid)}
							<DropdownMenu.RadioItem value={pool.guid} class="cursor-pointer"
								><span class="truncate">{pool.name}</span></DropdownMenu.RadioItem
							>
						{/each}
					</DropdownMenu.RadioGroup>
				</DropdownMenu.Content>
			</DropdownMenu.Root>
			<DropdownMenu.Root>
				<DropdownMenu.Trigger>
					{#snippet child({ props })}
						<Button
							variant="outline"
							size="sm"
							class="h-8 gap-2 font-normal"
							aria-label="Performance history range"
							{...props}
						>
							<span class="icon-[lucide--clock] text-muted-foreground size-4" aria-hidden="true"
							></span>
							{@render rangeLabel(selectedRange)}
							<span
								class="icon-[lucide--chevron-down] text-muted-foreground size-4"
								aria-hidden="true"
							></span>
						</Button>
					{/snippet}
				</DropdownMenu.Trigger>
				<DropdownMenu.Content align="end" class="w-44">
					<DropdownMenu.RadioGroup bind:value={selectedRange}>
						{#each ranges as range (range.value)}
							<DropdownMenu.RadioItem value={range.value} class="cursor-pointer"
								>{@render rangeLabel(range.value)}</DropdownMenu.RadioItem
							>
						{/each}
					</DropdownMenu.RadioGroup>
				</DropdownMenu.Content>
			</DropdownMenu.Root>
			<Button
				variant="outline"
				size="icon"
				class="size-8"
				onclick={refreshDashboard}
				disabled={busy}
				aria-label="Refresh storage dashboard"
				title="Refresh storage dashboard"
			>
				<span class="icon-[lucide--refresh-cw] size-4" class:animate-spin={busy} aria-hidden="true"
				></span>
			</Button>
		</div>
	</header>

	{#if attention}
		<div
			class="mt-4 flex items-start gap-3 rounded-lg border px-4 py-2.5 text-sm {summary.errors >
				0 || health === 'danger'
				? 'border-red-600/40 bg-red-600/10'
				: 'border-amber-500/40 bg-amber-500/10'}"
			role="status"
		>
			<span
				class="icon-[lucide--triangle-alert] mt-0.5 size-5 shrink-0 {summary.errors > 0 ||
				health === 'danger'
					? toneText.danger
					: toneText.warning}"
				aria-hidden="true"
			></span>
			<p class="min-w-0 break-words">{attention}</p>
		</div>
	{/if}

	<div class="mt-4 grid gap-4">
		{#if pools.length > 0}
			<div class="grid gap-4 @2xl:grid-cols-3">
				<Panel title="Pool Health" icon="icon-[mdi--shield-check-outline]">
					<p class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
						<span class="text-[28px] leading-8 font-semibold tracking-tight tabular-nums"
							>{summary.online} / {pools.length}</span
						>
						<span class="text-muted-foreground text-sm">Online</span>
					</p>
					<p class="mt-2 text-xs {toneText[health]}">
						{summary.errors > 0
							? `${summary.errors} Known Errors`
							: summary.statusUnavailable > 0
								? 'Device Status Incomplete'
								: health === 'success'
									? 'No Known Pool or Device Errors'
									: `Pool State: ${summary.health}`}
					</p>
					<p class="text-muted-foreground mt-auto pt-4 text-xs">
						{summary.dataVdevs} Data VDEVs · {summary.disks} Disks
					</p>
				</Panel>
				<Panel title="Capacity" icon="icon-[mdi--storage]">
					<p class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
						<span
							class="text-[28px] leading-8 font-semibold tracking-tight whitespace-nowrap tabular-nums"
							>{formatBytesBinary(summary.allocated)}</span
						>
						<span class="text-muted-foreground text-sm"
							>of {formatBytesBinary(summary.totalSize)}</span
						>
					</p>
					<p class="text-muted-foreground mt-1 text-xs">
						{summary.usedPercent.toFixed(1)}% Used · {formatBytesBinary(summary.free)} Free
					</p>
					<div class="mt-auto pt-4">
						<div
							class="bg-muted h-2 overflow-hidden rounded-full"
							role="meter"
							aria-label="Pool capacity used"
							aria-valuemin="0"
							aria-valuemax="100"
							aria-valuenow={Math.min(100, Math.max(0, summary.usedPercent))}
						>
							<div
								class="h-full rounded-full {toneMeter[capacity]}"
								style:width="{Math.min(100, Math.max(0, summary.usedPercent))}%"
							></div>
						</div>
					</div>
				</Panel>
				<Panel title="Pool Verification" icon="icon-[lets-icons--check-fill]">
					<p class="text-xl leading-8 font-semibold tracking-tight {toneText[verification.tone]}">
						{verification.value}
					</p>
					<p class="text-muted-foreground mt-1 text-xs">{verification.detail}</p>
					<p class="text-muted-foreground mt-auto pt-4 text-xs">{verification.footer}</p>
				</Panel>
			</div>
		{/if}

		{#if pools.length > 0}
			<Performance
				{pools}
				history={historyMatches ? history.current.data : null}
				seconds={rangeSeconds}
				loading={history.loading || rangePending}
				failed={!rangePending && Boolean(history.error)}
			/>
		{/if}

		<Pools {pools} />
		<Cache arc={snapshot.current.arc} />
	</div>
</div>
