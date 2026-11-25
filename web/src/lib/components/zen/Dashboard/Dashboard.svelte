<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import { getSimpleContainers } from '$lib/api/container/container';
	import { getBasicInfo } from '$lib/api/info/basic';
	import { getCPUInfo } from '$lib/api/info/cpu';
	import { getRAMInfo, getSwapInfo } from '$lib/api/info/ram';
	import { getNodeSummaryHistory } from '$lib/api/info/summary';
	import { getSimpleJails } from '$lib/api/jail/jail';
	import { getInterfaces } from '$lib/api/network/iface';
	import { getSimpleVMs } from '$lib/api/vm/vm';
	import { getZFSDashboardSnapshot } from '$lib/api/zfs/pool';
	import { capacityTone, statusTone } from '$lib/components/custom/ZFS/Dashboard/dashboard';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import { alerts } from '$lib/stores/interface.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { SimpleContainer } from '$lib/types/container/container';
	import type { BasicInfo } from '$lib/types/info/basic';
	import type { CPUInfo } from '$lib/types/info/cpu';
	import type { RAMInfo } from '$lib/types/info/ram';
	import type { NodeSummaryHistory } from '$lib/types/info/summary';
	import type { SimpleJail } from '$lib/types/jail/jail';
	import type { Iface } from '$lib/types/network/iface';
	import type { AvailableService } from '$lib/types/system/settings';
	import { DomainState, type SimpleVm } from '$lib/types/vm/vm';
	import type { ZFSDashboardSnapshot } from '$lib/types/zfs/pool';
	import { formatBitsPerSecondDecimal, formatBytesBinary } from '$lib/utils/bytes';
	import { getEnabledServicesForHostname } from '$lib/utils/enabled-services';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { mergeSummaryHistory } from '$lib/utils/info/summary';
	import { floatToNDecimals } from '$lib/utils/numbers';
	import { formatUptime } from '$lib/utils/time';
	import { resource, useInterval, watch } from 'runed';
	import Chart from './Chart.svelte';
	import { hardwareName, processorName, systemName } from './format';
	import Interfaces from './Interfaces.svelte';
	import Panel from './Panel.svelte';
	import Pools from './Pools.svelte';

	interface Data {
		hostname: string;
		basicInfo: BasicInfo;
		cpuInfo: CPUInfo;
		ramInfo: RAMInfo;
		swapInfo: RAMInfo;
		summaryHistory: NodeSummaryHistory & { reconciledAt?: number };
		snapshot: ZFSDashboardSnapshot | null;
		interfaces: Iface[] | APIResponse | null;
		vms: SimpleVm[] | null;
		jails: SimpleJail[] | null;
		containers: SimpleContainer[] | null;
		alertCount: number | null;
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	const initial = data;
	if (initial.alertCount != null) alerts.count = initial.alertCount;

	const historyReconcileInterval = 5 * 60 * 1000;

	function live<T>(key: string, fetch: () => Promise<T>, initialValue: T) {
		return resource(
			() => key,
			async (): Promise<unknown> => {
				const result = await fetch();
				if (result !== null) void updateCache(key, result, initial.hostname);
				return result;
			},
			{ initialValue: initialValue as unknown }
		) as { readonly current: T; refetch: () => Promise<unknown> };
	}

	let services = $derived(getEnabledServicesForHostname(initial.hostname));

	function quiet<T>(fetch: () => Promise<T>): Promise<T | null> {
		return fetch().catch(() => null);
	}

	function enabled<T>(service: AvailableService, fetch: () => Promise<T>): Promise<T | null> {
		return services.includes(service) ? quiet(fetch) : Promise.resolve(null);
	}

	async function loadInterfaces(): Promise<Iface[] | null> {
		const result = await getInterfaces({ hostname: initial.hostname });
		return isAPIResponse(result) ? null : result;
	}

	const basic = live('basic-info', getBasicInfo, initial.basicInfo);
	const cpu = live('cpu-info', () => getCPUInfo('current'), initial.cpuInfo);
	const ram = live('ram-info', () => getRAMInfo('current'), initial.ramInfo);
	const swap = live('swap-info', () => getSwapInfo('current'), initial.swapInfo);
	const snapshot = live(
		`zfs-dashboard-snapshot:${initial.hostname}`,
		() => quiet(() => getZFSDashboardSnapshot({ hostname: initial.hostname })),
		initial.snapshot
	);
	const interfaces = live(
		'network-interfaces',
		loadInterfaces,
		isAPIResponse(initial.interfaces) ? null : initial.interfaces
	);
	const vms = live(
		'simple-vms',
		() => enabled('virtualization', () => getSimpleVMs(initial.hostname)),
		initial.vms
	);
	const jails = live(
		'simple-jails',
		() => enabled('jails', () => getSimpleJails(initial.hostname)),
		initial.jails
	);
	const containers = live(
		'simple-containers',
		() => enabled('containers', () => getSimpleContainers(initial.hostname)),
		initial.containers
	);

	const history = resource(
		() => 'node-summary-history',
		async (key, _previousKey, { data, refetching, signal }): Promise<Data['summaryHistory']> => {
			const current = data ?? initial.summaryHistory;
			const full =
				refetching === 'full' ||
				Date.now() - (current.reconciledAt ?? 0) >= historyReconcileInterval;
			const incoming = await getNodeSummaryHistory(full ? undefined : current.cursors, {
				hostname: initial.hostname,
				signal
			});
			signal.throwIfAborted();
			const result = {
				...(full ? incoming : mergeSummaryHistory(current, incoming)),
				reconciledAt: full ? Date.now() : (current.reconciledAt ?? 0)
			};
			void updateCache(key, result, initial.hostname);
			return result;
		},
		{ initialValue: initial.summaryHistory, lazy: true }
	);

	function refreshFast() {
		cpu.refetch();
		ram.refetch();
	}

	function refreshSteady() {
		swap.refetch();
		snapshot.refetch();
		void history.refetch('delta');
	}

	function refreshSlow() {
		basic.refetch();
		interfaces.refetch();
		vms.refetch();
		jails.refetch();
		containers.refetch();
	}

	useInterval(2000, { callback: () => storage.visible && refreshFast() });
	useInterval(10000, { callback: () => storage.visible && refreshSteady() });
	useInterval(30000, { callback: () => storage.visible && refreshSlow() });

	watch(
		() => storage.visible,
		(visible) => {
			if (!visible) return;
			refreshFast();
			refreshSteady();
			refreshSlow();
		},
		{ lazy: true }
	);

	const minute = 60 * 1000;
	const spans: Record<string, number> = {
		'15m': 15 * minute,
		'1h': 60 * minute,
		'24h': 24 * 60 * minute,
		'7d': 7 * 24 * 60 * minute
	};
	let range = $state('15m');

	let period = $derived.by(() => {
		void history.current;
		const end = Date.now();
		return { start: end - spans[range], end };
	});

	function within<T extends { createdAt: string }>(points: T[], value: (point: T) => number) {
		return points
			.map((point) => ({ date: Date.parse(point.createdAt), value: value(point) }))
			.filter((point) => point.date >= period.start && point.date <= period.end);
	}

	let cpuSeries = $derived([
		{
			name: 'Usage',
			color: 'var(--zen-accent)',
			points: within(history.current.cpu, (point) => point.usage)
		}
	]);

	let networkSeries = $derived([
		{
			name: 'Receive',
			color: 'var(--zen-accent)',
			points: within(history.current.network, (point) => Math.max(0, point.receivedBytes))
		},
		{
			name: 'Transmit',
			color: 'var(--zen-contrast)',
			points: within(history.current.network, (point) => Math.max(0, point.sentBytes))
		}
	]);

	let latestNetwork = $derived(history.current.network.at(-1));

	let memoryUsed = $derived((ram.current.total * ram.current.usedPercent) / 100);
	let memoryCache = $derived(Math.min(ram.current.arc || 0, memoryUsed));
	let memoryParts = $derived(
		[
			{ label: 'In Use', style: 'bg-zen-accent', bytes: memoryUsed - memoryCache },
			{ label: 'ZFS Cache', style: 'bg-teal-600 dark:bg-teal-400', bytes: memoryCache },
			{ label: 'Free', style: 'bg-muted-foreground/25', bytes: ram.current.total - memoryUsed }
		].filter((part) => part.bytes > 0)
	);
	let swapUsed = $derived((swap.current.total * swap.current.usedPercent) / 100);

	let pools = $derived(snapshot.current?.pools ?? null);
	let compactSystem = $derived(pools !== null && pools.length > 0 && pools.length <= 2);
	let troubledPool = $derived(
		pools?.find(
			(pool) =>
				statusTone(pool.state, pool.errors.read + pool.errors.write + pool.errors.checksum, 0) !==
				'success'
		)
	);
	let fullPool = $derived(
		pools?.find(
			(pool) => pool.size > 0 && capacityTone((pool.allocated / pool.size) * 100) !== 'neutral'
		)
	);
	let condition = $derived<'degraded' | 'attention' | 'healthy'>(
		troubledPool ? 'degraded' : fullPool || alerts.count > 0 ? 'attention' : 'healthy'
	);
	const conditionStyle = {
		healthy: {
			icon: 'icon-[lucide--circle-check] text-green-600 dark:text-green-500',
			banner: ''
		},
		attention: {
			icon: 'icon-[lucide--triangle-alert] text-amber-500',
			banner: 'border-amber-500/40 bg-amber-500/10'
		},
		degraded: {
			icon: 'icon-[lucide--circle-x] text-red-600 dark:text-red-500',
			banner: 'border-red-600/40 bg-red-600/10'
		}
	};

	let workloads = $derived(
		[
			{
				label: 'Virtual Machines',
				icon: 'icon-[lucide--monitor]',
				states: vms.current?.map((vm) => vm.state === DomainState.DomainRunning)
			},
			{
				label: 'Jails',
				icon: 'icon-[lucide--box]',
				states: jails.current?.map((jail) => jail.state === 'ACTIVE')
			},
			{
				label: 'Containers',
				icon: 'icon-[lucide--container]',
				states: containers.current?.map((container) => container.state === 'running')
			}
		].flatMap(({ label, icon, states }) =>
			states ? [{ label, icon, total: states.length, running: states.filter(Boolean).length }] : []
		)
	);
	let workloadsTotal = $derived(workloads.reduce((sum, workload) => sum + workload.total, 0));
	let workloadsRunning = $derived(workloads.reduce((sum, workload) => sum + workload.running, 0));

	let uptime = $derived(formatUptime(basic.current.uptime).split(' ').slice(0, 2).join(' '));
	let details = $derived(
		[
			{ label: 'Hostname', value: basic.current.hostname || initial.hostname },
			{
				label: 'Hardware',
				value: hardwareName(basic.current.hardware?.vendor, basic.current.hardware?.product)
			},
			{
				label: 'System',
				value: [systemName(basic.current.os), basic.current.bootMode].filter(Boolean).join(' · ')
			},
			{ label: 'Architecture', value: compactSystem ? '' : basic.current.architecture },
			{ label: 'Processor', value: compactSystem ? '' : processorName(cpu.current.name) },
			{ label: 'Sylve Version', value: basic.current.sylveVersion },
			{ label: 'Uptime', value: uptime },
			{ label: 'Load Average', value: compactSystem ? '' : basic.current.loadAverage },
			{ label: 'Serial Number', value: compactSystem ? '' : basic.current.hardware?.serial }
		].filter((detail) => detail.value)
	);
</script>

{#snippet rangeLabel(key: string)}
	{#if key === '15m'}
		Last 15 minutes
	{:else if key === '1h'}
		Last hour
	{:else if key === '24h'}
		Last 24 hours
	{:else}
		Last 7 days
	{/if}
{/snippet}

<div class="@container/overview px-4 pt-3 pb-4 md:px-5 md:pb-5">
	<header class="flex flex-wrap items-center justify-between gap-3">
		<div class="flex min-w-0 items-center gap-3">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
				aria-hidden="true"
			>
				<span class="icon-[lucide--layout-dashboard] size-5"></span>
			</span>
			<h1 class="text-2xl font-semibold tracking-tight">Overview</h1>
		</div>

		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button variant="outline" size="sm" class="h-8 gap-2 font-normal" {...props}>
						<span class="icon-[lucide--clock] text-muted-foreground size-4"></span>
						{@render rangeLabel(range)}
						<span class="icon-[lucide--chevron-down] text-muted-foreground size-4"></span>
					</Button>
				{/snippet}
			</DropdownMenu.Trigger>
			<DropdownMenu.Content align="end" class="w-44">
				<DropdownMenu.RadioGroup bind:value={range}>
					{#each Object.keys(spans) as key (key)}
						<DropdownMenu.RadioItem value={key} class="cursor-pointer">
							{@render rangeLabel(key)}
						</DropdownMenu.RadioItem>
					{/each}
				</DropdownMenu.RadioGroup>
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</header>

	{#if condition !== 'healthy'}
		<div
			class="mt-4 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border px-4 py-2.5 text-sm {conditionStyle[
				condition
			].banner}"
			role="status"
		>
			<span class="{conditionStyle[condition].icon} size-5 shrink-0" aria-hidden="true"></span>
			<p class="min-w-0 flex-1">
				{#if troubledPool}
					Pool {troubledPool.name} is {troubledPool.state.toLowerCase()}.
				{:else if fullPool}
					Pool {fullPool.name} is running out of space.
				{:else if alerts.count === 1}
					1 alert is waiting for review.
				{:else}
					{alerts.count} alerts are waiting for review.
				{/if}
			</p>
			{#if alerts.count > 0}
				<Button variant="outline" size="sm" class="h-7" onclick={() => (alerts.open = true)}>
					View alerts
				</Button>
			{/if}
		</div>
	{/if}

	<div class="mt-4 grid gap-4">
		<div class="grid gap-4 @2xl:grid-cols-3">
			<Panel title="CPU" icon="icon-[lucide--cpu]">
				{#snippet aside()}
					{#if cpu.current.temperature}{Math.round(cpu.current.temperature)} °C{/if}
				{/snippet}
				<p class="text-[28px] leading-8 font-semibold tracking-tight tabular-nums">
					{floatToNDecimals(cpu.current.usage, 1)}%
				</p>
				<p class="text-muted-foreground mt-1 flex flex-wrap gap-x-1.5 text-xs">
					{#if cpu.current.name}
						<span>{processorName(cpu.current.name)}</span>
						<span aria-hidden="true">·</span>
					{/if}
					<span>{cpu.current.logicalCores} Threads</span>
				</p>
				<div class="mt-auto pt-4">
					<Chart
						series={cpuSeries}
						format={(value) => `${floatToNDecimals(value, 1)}%`}
						start={period.start}
						end={period.end}
						floor={5}
						limit={100}
					/>
				</div>
			</Panel>

			<Panel title="Memory" icon="icon-[lucide--memory-stick]">
				<p class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
					<span
						class="text-[28px] leading-8 font-semibold tracking-tight whitespace-nowrap tabular-nums"
					>
						{formatBytesBinary(memoryUsed)}
					</span>
					<span class="text-muted-foreground text-sm whitespace-nowrap tabular-nums">
						of {formatBytesBinary(ram.current.total)}
					</span>
				</p>
				<p class="text-muted-foreground mt-1 flex flex-wrap gap-x-1.5 text-xs">
					<span class="tabular-nums">{floatToNDecimals(ram.current.usedPercent, 0)}% used</span>
					<span aria-hidden="true">·</span>
					<span>Includes ZFS Cache</span>
				</p>
				<div class="mt-auto pt-4">
					<div class="flex h-2 gap-0.5 overflow-hidden rounded-full">
						{#each memoryParts as part (part.label)}
							<div
								class="h-full {part.style}"
								style:width="{(part.bytes / ram.current.total) * 100}%"
							></div>
						{/each}
					</div>
					<ul class="mt-3 space-y-1.5 text-xs">
						{#each memoryParts as part (part.label)}
							<li class="flex items-center gap-2">
								<span class="size-2 shrink-0 rounded-full {part.style}"></span>
								<span class="text-muted-foreground">{part.label}</span>
								<span class="ml-auto tabular-nums">{formatBytesBinary(part.bytes)}</span>
							</li>
						{/each}
					</ul>
					{#if swapUsed > 0}
						<div class="mt-3 border-t pt-3">
							<div class="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-xs">
								<h3 class="font-medium">Swap Used</h3>
								<span class="whitespace-nowrap tabular-nums">
									{formatBytesBinary(swapUsed)}
									<span class="text-muted-foreground"
										>of {formatBytesBinary(swap.current.total)}</span
									>
								</span>
							</div>
							<div
								class="bg-muted-foreground/25 mt-2 h-1.5 overflow-hidden rounded-full"
								role="meter"
								aria-label="Swap Used"
								aria-valuemin="0"
								aria-valuemax="100"
								aria-valuenow={Math.round(swap.current.usedPercent)}
							>
								<div
									class="h-full rounded-full bg-violet-600 dark:bg-violet-400"
									style:width="{Math.min(100, swap.current.usedPercent)}%"
								></div>
							</div>
						</div>
					{/if}
				</div>
			</Panel>

			<Panel title="Network" icon="icon-[lucide--network]">
				<dl class="space-y-1">
					<div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5">
						<dt class="text-muted-foreground flex items-center gap-1.5 text-xs">
							<span class="bg-zen-accent h-0.5 w-3 shrink-0 rounded-full"></span>
							Receive
						</dt>
						<dd class="text-lg font-semibold tracking-tight whitespace-nowrap tabular-nums">
							{formatBitsPerSecondDecimal(latestNetwork?.receivedBytes ?? 0)}
						</dd>
					</div>
					<div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5">
						<dt class="text-muted-foreground flex items-center gap-1.5 text-xs">
							<span class="bg-zen-contrast h-0.5 w-3 shrink-0 rounded-full"></span>
							Transmit
						</dt>
						<dd class="text-lg font-semibold tracking-tight whitespace-nowrap tabular-nums">
							{formatBitsPerSecondDecimal(latestNetwork?.sentBytes ?? 0)}
						</dd>
					</div>
				</dl>
				<div class="mt-auto pt-4">
					<Chart
						series={networkSeries}
						format={(value) => formatBitsPerSecondDecimal(value)}
						start={period.start}
						end={period.end}
					/>
				</div>
			</Panel>
		</div>

		<div class="grid gap-4 {pools ? '@4xl:grid-cols-3' : ''}">
			{#if pools}
				<Pools {pools} class="@4xl:col-span-2 {compactSystem ? '' : '@4xl:[contain:size]'}" />
			{/if}
			<Panel title="System" icon="icon-[lucide--server]">
				<dl class="space-y-2 text-[13px]">
					{#each details as detail (detail.label)}
						<div class="flex items-baseline justify-between gap-4">
							<dt class="text-muted-foreground shrink-0">{detail.label}</dt>
							<dd class="min-w-0 text-right break-words">{detail.value}</dd>
						</div>
					{/each}
				</dl>
			</Panel>
		</div>

		<div class="grid gap-4 {interfaces.current ? '@4xl:grid-cols-3' : ''}">
			{#if interfaces.current}
				<Interfaces interfaces={interfaces.current} class="@4xl:col-span-2" />
			{/if}
			<Panel title="Workloads" icon="icon-[lucide--boxes]">
				{#snippet aside()}
					{#if workloads.length > 0}
						<span class="text-foreground tabular-nums">{workloadsRunning}</span>
						of {workloadsTotal} Running
					{/if}
				{/snippet}
				{#if workloads.length === 0}
					<p class="text-muted-foreground text-sm">No workload services are enabled.</p>
				{:else}
					<ul class="divide-y text-[13px]">
						{#each workloads as workload (workload.label)}
							<li class="flex items-center gap-2.5 py-2.5 last:pb-0">
								<span class="{workload.icon} text-muted-foreground size-4 shrink-0"></span>
								<span>{workload.label}</span>
								<span class="text-muted-foreground ml-auto shrink-0 tabular-nums">
									<span class="text-foreground font-medium">{workload.running}</span>
									/ {workload.total}
								</span>
							</li>
						{/each}
					</ul>
				{/if}
			</Panel>
		</div>
	</div>
</div>
