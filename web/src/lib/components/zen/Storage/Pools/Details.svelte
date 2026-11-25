<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Disk } from '$lib/types/disk/disk';
	import type { Zpool, ZpoolStatusPool } from '$lib/types/zfs/pool';
	import { resource, useInterval, watch } from 'runed';
	import { onDestroy, untrack } from 'svelte';
	import Panel from '../../Dashboard/Panel.svelte';
	import Capacity from './Capacity.svelte';
	import Notice from './Notice.svelte';
	import {
		deviceErrorSummary,
		deviceLeaves,
		poolHref,
		poolLayout,
		poolUsage,
		topologyGroups
	} from './pools';
	import State from './State.svelte';
	import Properties from './Properties.svelte';
	import Scrub from './Scrub.svelte';
	import { loadPoolStatus } from './status';
	import Topology from './Topology.svelte';

	let {
		hostname,
		pool,
		pools,
		disks,
		inventoryUnavailable,
		refreshPools,
		refreshing,
		initialStatus,
		onStatus
	}: {
		hostname: string;
		pool: Zpool;
		pools: Zpool[];
		disks: Disk[];
		inventoryUnavailable: boolean;
		refreshPools: () => Promise<void>;
		refreshing: boolean;
		initialStatus: ZpoolStatusPool | null;
		onStatus: (status: ZpoolStatusPool) => void;
	} = $props();
	const identity = untrack(() => ({ hostname, guid: pool.guid }));
	const cachedStatus = untrack(() =>
		initialStatus?.pool_guid === identity.guid ? initialStatus : null
	);
	const lifetime = new AbortController();
	let scrubActive = $state(false);
	let refreshVersion = $state(0);
	let statusRefreshing = $state(false);
	let statusRevision = 0;
	const status = resource(
		() => identity.guid,
		async (guid, _previous, { signal }): Promise<ZpoolStatusPool | null> => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			try {
				const result = await loadPoolStatus(identity.hostname, guid, {
					signal: requestSignal,
					fresh: true
				});
				requestSignal.throwIfAborted();
				onStatus(result);
				return result;
			} catch (error) {
				requestSignal.throwIfAborted();
				throw error;
			}
		},
		{ initialValue: cachedStatus }
	);
	watch(
		() => initialStatus,
		(sample) => {
			if (sample?.pool_guid === identity.guid && !status.current) status.mutate(sample);
		},
		{ lazy: true }
	);
	const interval = useInterval(() => (scrubActive ? 5_000 : storage.idle ? 30_000 : 10_000), {
		callback: () => {
			if (storage.visible && !status.loading && !statusRefreshing) void refreshStatus();
		}
	});
	watch(
		[() => storage.visible, () => storage.idle],
		([visible, idle]) => {
			if (visible && !idle && !status.loading && !statusRefreshing) void refreshStatus();
		},
		{ lazy: true }
	);
	onDestroy(() => {
		lifetime.abort();
		interval.pause();
	});
	let detail = $derived(status.current ?? null);
	let groups = $derived(topologyGroups(pool, detail, disks));
	let devices = $derived(groups.flatMap((group) => deviceLeaves(group.nodes)));
	let errors = $derived(deviceErrorSummary(groups.flatMap((group) => group.nodes)));

	async function refreshStatus() {
		const version = ++statusRevision;
		statusRefreshing = true;
		try {
			return await status.refetch();
		} finally {
			if (version === statusRevision) statusRefreshing = false;
		}
	}
	async function refresh() {
		refreshVersion++;
		await Promise.all([refreshPools(), refreshStatus()]);
	}
</script>

<header class="mb-5 flex items-center justify-between gap-3">
	<div class="flex min-w-0 flex-1 items-center gap-3">
		<span
			class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
			aria-hidden="true"
		>
			<span class="icon-[bi--hdd-stack-fill] size-5"></span>
		</span>
		<Breadcrumb.Root class="min-w-0">
			<Breadcrumb.List class="items-baseline gap-2 sm:gap-2">
				<Breadcrumb.Item>
					<span class="text-lg font-medium tracking-tight sm:text-xl">Storage</span>
				</Breadcrumb.Item>
				<Breadcrumb.Separator class="text-muted-foreground/60 self-center" />
				<Breadcrumb.Item>
					<Breadcrumb.Link
						href={poolHref(hostname)}
						class="focus-visible:ring-ring rounded-sm text-lg font-medium tracking-tight focus-visible:ring-2 focus-visible:outline-none sm:text-xl"
						aria-label="Back to Pools"
					>
						Pools
					</Breadcrumb.Link>
				</Breadcrumb.Item>
				<Breadcrumb.Separator class="text-muted-foreground/60 self-center" />
				<Breadcrumb.Item class="min-w-0 max-w-full">
					<h1 class="min-w-0 text-lg leading-7 tracking-tight break-all sm:text-xl">
						<Breadcrumb.Page class="font-semibold">{pool.name}</Breadcrumb.Page>
					</h1>
				</Breadcrumb.Item>
			</Breadcrumb.List>
		</Breadcrumb.Root>
	</div>
	<Button
		variant="outline"
		size="icon"
		class="size-9 shrink-0"
		onclick={refresh}
		disabled={status.loading || statusRefreshing || refreshing}
		aria-label="Refresh pool details"
		><span
			class="icon-[lucide--refresh-cw] size-4"
			class:animate-spin={status.loading || statusRefreshing || refreshing}
			aria-hidden="true"
		></span></Button
	>
</header>

{#if status.error || !detail}
	<p
		class="mb-4 rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-2.5 text-sm"
		role="status"
	>
		{detail
			? 'Detailed status could not be refreshed. Showing its last successful sample.'
			: 'Detailed ZFS status could not be loaded. Topology is shown from the pool inventory; error counters are unavailable. Use Refresh to try again.'}
	</p>
{/if}
{#if inventoryUnavailable}<p class="text-muted-foreground mb-4 text-xs" role="status">
		Disk identification data is unavailable. Device topology and health still come from ZFS.
	</p>{/if}

<div class="grid gap-4">
	<div class="grid gap-4 @xs:grid-cols-2 @2xl:grid-cols-3">
		<Panel title="Capacity" icon="icon-[mdi--storage]" class="@xs:col-span-2 @2xl:col-span-1">
			<div class="mb-3 flex items-baseline gap-2">
				<p class="text-[28px] leading-8 font-semibold tracking-tight tabular-nums">
					{poolUsage(pool).toFixed(1)}%
				</p>
				<span class="text-muted-foreground text-sm">Used</span>
			</div>
			<Capacity {pool} summary />
		</Panel>
		<Panel title="Layout" icon="icon-[tabler--topology-bus]">
			<p class="mb-3 text-[28px] leading-8 font-semibold tracking-tight">{poolLayout(pool)}</p>
			<p class="text-xs">
				{Object.keys(pool.vdevs).length} Data {Object.keys(pool.vdevs).length === 1
					? 'VDEV'
					: 'VDEVs'} · {devices.length}&nbsp;{devices.length === 1 ? 'Device' : 'Devices'}
			</p>
			<p class="text-muted-foreground mt-2 text-xs">
				Fragmentation {pool.fragmentation.toFixed(0)}% · Dedup {pool.dedupRatio.toFixed(2)}×
			</p>
		</Panel>
		<Panel title="Health" icon="icon-[mdi--shield-check-outline]">
			<State value={detail?.state || pool.state} prominent />
			<p class="mt-3 flex items-baseline gap-1.5 text-xs">
				<span
					class="font-medium tabular-nums {errors.total > 0
						? 'text-red-600 dark:text-red-400'
						: ''}"
				>
					{errors.complete || errors.total > 0 ? errors.total.toLocaleString() : '—'}
				</span>
				<span class="text-muted-foreground">Device Errors</span>
			</p>
			<p class="text-muted-foreground mt-2 text-xs">
				{!errors.complete
					? 'Some Error Counters Are Unavailable'
					: errors.total > 0
						? 'Review the Affected Devices Below'
						: 'No Known Device Errors'}
			</p>
		</Panel>
	</div>
	{#if detail?.status?.trim() || detail?.action?.trim()}
		<Notice status={detail} poolState={detail.state || pool.state} errors={errors.total} />
	{/if}
	<Topology
		{hostname}
		{pool}
		{pools}
		status={detail}
		{disks}
		{inventoryUnavailable}
		statusUnavailable={Boolean(status.error)}
		scanBusy={scrubActive}
		onChanged={refresh}
	/>
	<Scrub
		{hostname}
		{pool}
		status={detail}
		statusLoading={status.loading || statusRefreshing}
		statusError={Boolean(status.error)}
		{refreshStatus}
		{refreshVersion}
		bind:active={scrubActive}
	/>
	<Properties {pool} />
</div>
