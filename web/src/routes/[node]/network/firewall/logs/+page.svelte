<script lang="ts">
	import { getFirewallLiveLogs } from '$lib/api/network/firewall';
	import TreeTableInfinite, {
		type InfiniteTableControl
	} from '$lib/components/custom/TreeTableInfinite.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Separator } from '$lib/components/ui/separator';
	import * as Popover from '$lib/components/ui/popover/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { FirewallLiveHitEvent } from '$lib/types/network/firewall';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { handleAPIError } from '$lib/utils/http';
	import { renderWithIcon } from '$lib/utils/table';
	import { convertDbTime } from '$lib/utils/time';
	import { onMount } from 'svelte';
	import { watch } from 'runed';
	import type { CellComponent } from 'tabulator-tables';
	import { toast } from 'svelte-sonner';

	let cursor = 0;
	let nextPollCursor = 0;
	let updating = false;
	let fetchIntent: 'auto' | 'manual' = 'auto';

	let sourceStatus = $state<'ok' | 'unavailable'>('unavailable');
	let stale = $state(false);
	let paused = $state(false);

	let logControl = $state<InfiniteTableControl | null>(null);
	let filterType = $state<'traffic' | 'nat' | null>(null);
	let filterAction = $state<string | null>(null);
	let filterDirection = $state<'in' | 'out' | null>(null);
	let filterQuery = $state('');
	let filterOpen = $state(false);

	const activeFilterCount = $derived(
		(filterType ? 1 : 0) +
			(filterAction ? 1 : 0) +
			(filterDirection ? 1 : 0) +
			(filterQuery.trim() ? 1 : 0)
	);

	function clearAllFilters() {
		filterType = null;
		filterAction = null;
		filterDirection = null;
		filterQuery = '';
	}

	function applyTableFilter() {
		const type = filterType;
		const action = filterAction;
		const direction = filterDirection;
		const query = filterQuery.trim().toLowerCase();
		const hasFilters = type || action || direction || query;

		if (!hasFilters) {
			logControl?.setFilter(null);
		} else {
			logControl?.setFilter((row) => {
				if (type && row.ruleType !== type) return false;
				if (action && row.action !== action) return false;
				if (direction && row.direction !== direction) return false;
				if (query) {
					const haystack = [row.ruleName, row['interface'], row.rawLine]
						.filter(Boolean)
						.join(' ')
						.toLowerCase();
					if (!haystack.includes(query)) return false;
				}
				return true;
			});
		}
	}

	watch(
		() => filterType,
		() => applyTableFilter()
	);
	watch(
		() => filterAction,
		() => applyTableFilter()
	);
	watch(
		() => filterDirection,
		() => applyTableFilter()
	);
	watch(
		() => filterQuery,
		() => applyTableFilter()
	);
	watch(
		() => logControl,
		() => applyTableFilter()
	);

	function typeBadge(ruleType: string): string {
		const isTraffic = ruleType === 'traffic';
		const color = isTraffic
			? 'text-blue-400 border-blue-400/50'
			: 'text-amber-400 border-amber-400/50';
		return `<span class="inline-flex items-center rounded border px-1.5 py-0.5 text-xs font-mono ${color} leading-tight">${ruleType.toUpperCase()}</span>`;
	}

	function actionCell(action: string | null, direction: string | null): string {
		if (!action) return '<span class="text-muted-foreground">-</span>';
		const isPass = action === 'pass';
		const isRdr = action === 'rdr';
		const icon = isPass
			? 'mdi:check-circle-outline'
			: isRdr
				? 'mdi:call-split'
				: 'mdi:close-octagon-outline';
		const color = isPass ? 'text-green-500' : isRdr ? 'text-purple-400' : 'text-red-400';
		const actionPart = renderWithIcon(icon, action.toUpperCase(), color);
		if (!direction) return actionPart;
		const dirPart = renderWithIcon(
			direction === 'in' ? 'mdi:arrow-down-circle-outline' : 'mdi:arrow-up-circle-outline',
			direction.toUpperCase(),
			direction === 'in' ? 'text-blue-400' : 'text-orange-400'
		);
		return `<span class="inline-flex items-center gap-1.5">${actionPart}<span class="text-muted-foreground/50">·</span>${dirPart}</span>`;
	}

	const columns: Column[] = [
		{ field: 'cursor', title: 'cursor', visible: false },
		{ field: 'direction', title: 'direction', visible: false },
		{
			field: 'timestamp',
			title: 'Time',
			width: '12%',
			formatter: (cell: CellComponent) => convertDbTime(cell.getValue())
		},
		{
			field: 'ruleType',
			title: 'Type',
			width: '7%',
			formatter: (cell: CellComponent) => typeBadge(cell.getValue())
		},
		{
			field: 'ruleName',
			title: 'Rule',
			width: '15%',
			formatter: (cell: CellComponent) => {
				const d = cell.getRow().getData();
				const name = d.ruleName || `Rule #${d.ruleId}`;
				return `<span class="font-medium">${name}</span><span class="text-muted-foreground ml-2 text-xs">#${d.ruleId}</span>`;
			}
		},
		{
			field: 'action',
			title: 'Action',
			width: '10%',
			formatter: (cell: CellComponent) => {
				const d = cell.getRow().getData();
				return actionCell(cell.getValue(), d.direction);
			}
		},
		{
			field: 'interface',
			title: 'Interface',
			width: '10%',
			formatter: (cell: CellComponent) => {
				const v = cell.getValue();
				if (!v) return '<span class="text-muted-foreground">-</span>';
				return renderWithIcon('mdi:ethernet', v, 'text-indigo-400');
			}
		},
		{
			field: 'bytes',
			title: 'Bytes',
			width: '8%',
			formatter: (cell: CellComponent) => formatBytesBinary(cell.getValue(), { fallback: '0 B' })
		},
		{
			field: 'rawLine',
			title: 'Details',
			formatter: (cell: CellComponent) => {
				const v = cell.getValue();
				if (!v) return '<span class="text-muted-foreground">-</span>';
				return `<span class="font-mono text-xs break-all">${v}</span>`;
			}
		}
	];

	function hitToRow(hit: FirewallLiveHitEvent): Row {
		return {
			id: hit.cursor,
			cursor: hit.cursor,
			timestamp: hit.timestamp,
			ruleType: hit.ruleType,
			ruleId: hit.ruleId,
			ruleName: hit.ruleName,
			action: hit.action,
			direction: hit.direction,
			interface: hit.interface,
			bytes: hit.bytes,
			rawLine: hit.rawLine
		};
	}

	async function refreshLiveLogs(intent: 'auto' | 'manual' = 'auto') {
		if (paused && intent === 'auto') return;
		if (updating) return;
		updating = true;
		fetchIntent = intent;
		try {
			const result = await getFirewallLiveLogs(cursor, 200);
			if ('status' in result) {
				stale = true;
				if (fetchIntent === 'manual') {
					handleAPIError(result);
					toast.error('Failed to refresh firewall logs', { position: 'bottom-center' });
				}
				return;
			}
			stale = false;
			sourceStatus = result.sourceStatus;

			const items = result.items ?? [];
			if (items.length > 0) {
				logControl?.push(items.map(hitToRow));
			}

			nextPollCursor = result.nextCursor ?? cursor;
			cursor = Math.max(cursor, nextPollCursor);
		} finally {
			fetchIntent = 'auto';
			updating = false;
		}
	}

	onMount(() => {
		void refreshLiveLogs('auto');

		const refreshInterval = setInterval(() => {
			if (document.visibilityState === 'visible') {
				void refreshLiveLogs('auto');
			}
		}, 1000);

		const onVisibilityChange = () => {
			if (document.visibilityState === 'visible') {
				void refreshLiveLogs('auto');
			}
		};
		document.addEventListener('visibilitychange', onVisibilityChange);

		return () => {
			clearInterval(refreshInterval);
			document.removeEventListener('visibilitychange', onVisibilityChange);
		};
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<button
			class="inline-flex h-6 cursor-pointer items-center gap-1.5 rounded border px-2 text-xs transition-colors {paused
				? 'border-amber-500/40 text-amber-400 hover:border-amber-500/70'
				: stale || sourceStatus !== 'ok'
					? 'border-amber-500/40 text-amber-400 hover:border-amber-500/70'
					: 'border-emerald-500/40 text-emerald-400 hover:border-emerald-500/70'}"
			onclick={() => (paused = !paused)}
			title={paused ? 'Resume live log streaming' : 'Pause live log streaming'}
		>
			{#if paused}
				<span class="icon-[mdi--pause-circle-outline] h-3.5 w-3.5 shrink-0"></span>
				Paused
			{:else}
				<span
					class="inline-block h-1.5 w-1.5 shrink-0 rounded-full {stale || sourceStatus !== 'ok'
						? 'bg-amber-500'
						: 'bg-emerald-500'}"
				></span>
				{stale || sourceStatus !== 'ok' ? 'Connecting' : 'Connected'}
			{/if}
		</button>

		<Popover.Root bind:open={filterOpen}>
			<Popover.Trigger class="inline-flex h-6 items-center">
				<Button
					size="sm"
					variant={activeFilterCount > 0 ? 'default' : 'outline'}
					class="h-full gap-1"
				>
					<span class="icon-[mdi--filter-outline] h-3.5 w-3.5"></span>
					<span class="text-xs">Filter</span>
					{#if activeFilterCount > 0}
						<span
							class="bg-primary-foreground text-primary inline-flex h-4 min-w-4 items-center justify-center rounded-full px-0.5 text-[10px] font-bold"
							>{activeFilterCount}</span
						>
					{/if}
				</Button>
			</Popover.Trigger>
			<Popover.Content align="start" class="w-56 p-0">
				<div class="space-y-3 p-3">
					<div>
						<p
							class="text-muted-foreground mb-1.5 text-[11px] font-medium uppercase tracking-wider"
						>
							Rule Type
						</p>
						<div class="flex gap-1.5">
							{#each ['traffic', 'nat'] as type (type)}
								<Button
									size="sm"
									variant={filterType === type ? 'default' : 'secondary'}
									class="h-6 px-2 text-xs bg-muted! dark:bg-secondary!"
									onclick={() =>
										(filterType = filterType === type ? null : (type as 'traffic' | 'nat'))}
									>{type.toUpperCase()}</Button
								>
							{/each}
						</div>
					</div>
					<Separator />
					<div>
						<p
							class="text-muted-foreground mb-1.5 text-[11px] font-medium uppercase tracking-wider"
						>
							Action
						</p>
						<div class="flex gap-1.5">
							{#each ['pass', 'block'] as action (action)}
								<Button
									size="sm"
									variant={filterAction === action ? 'default' : 'secondary'}
									class="h-6 px-2 text-xs bg-muted! dark:bg-secondary!"
									onclick={() => (filterAction = filterAction === action ? null : action)}
									>{action.toUpperCase()}</Button
								>
							{/each}
						</div>
					</div>
					<Separator />
					<div>
						<p
							class="text-muted-foreground mb-1.5 text-[11px] font-medium uppercase tracking-wider"
						>
							Direction
						</p>
						<div class="flex gap-1.5">
							{#each ['in', 'out'] as dir (dir)}
								<Button
									size="sm"
									variant={filterDirection === dir ? 'default' : 'secondary'}
									class="h-6 px-2 text-xs bg-muted! dark:bg-secondary!"
									onclick={() =>
										(filterDirection = filterDirection === dir ? null : (dir as 'in' | 'out'))}
									>{dir.toUpperCase()}</Button
								>
							{/each}
						</div>
					</div>
					<Separator />
					<div>
						<p
							class="text-muted-foreground mb-1.5 text-[11px] font-medium uppercase tracking-wider"
						>
							Search
						</p>
						<Input
							bind:value={filterQuery}
							placeholder="Rule, interface, raw log…"
							class="h-7 text-xs bg-muted! dark:bg-secondary!"
						/>
					</div>
				</div>
				{#if activeFilterCount > 0}
					<div class="border-t p-2">
						<Button
							size="sm"
							variant="ghost"
							class="text-muted-foreground h-6 w-full text-xs"
							onclick={clearAllFilters}>Clear all filters</Button
						>
					</div>
				{/if}
			</Popover.Content>
		</Popover.Root>

		{#if filterType}
			<Badge
				variant="secondary"
				class="h-5 cursor-pointer gap-0.5 px-1.5 text-xs font-normal"
				onclick={() => (filterType = null)}
				><span class="text-muted-foreground">type:</span>{filterType}<span
					class="icon-[mdi--close] ml-0.5 h-3 w-3"
				></span></Badge
			>
		{/if}
		{#if filterAction}
			<Badge
				variant="secondary"
				class="h-5 cursor-pointer gap-0.5 px-1.5 text-xs font-normal"
				onclick={() => (filterAction = null)}
				><span class="text-muted-foreground">action:</span>{filterAction}<span
					class="icon-[mdi--close] ml-0.5 h-3 w-3"
				></span></Badge
			>
		{/if}
		{#if filterDirection}
			<Badge
				variant="secondary"
				class="h-5 cursor-pointer gap-0.5 px-1.5 text-xs font-normal"
				onclick={() => (filterDirection = null)}
				><span class="text-muted-foreground">dir:</span>{filterDirection}<span
					class="icon-[mdi--close] ml-0.5 h-3 w-3"
				></span></Badge
			>
		{/if}
		{#if filterQuery.trim()}
			<Badge
				variant="secondary"
				class="h-5 max-w-36 cursor-pointer gap-0.5 px-1.5 text-xs font-normal"
				onclick={() => (filterQuery = '')}
				><span class="text-muted-foreground shrink-0">search:</span><span class="truncate"
					>{filterQuery.trim()}</span
				><span class="icon-[mdi--close] ml-0.5 h-3 w-3 shrink-0"></span></Badge
			>
		{/if}

		<div class="ml-auto flex items-center gap-2">
			<Button onclick={() => refreshLiveLogs('manual')} size="sm" variant="outline" class="h-6">
				<div class="flex items-center">
					<span class="icon-[mdi--refresh] h-4 w-4"></span>
				</div>
			</Button>
			<Button
				onclick={() => {
					logControl?.clear();
				}}
				size="sm"
				variant="outline"
				class="h-6"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--broom] h-4 w-4"></span>
				</div>
			</Button>
		</div>
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTableInfinite
			{columns}
			name="tt-firewall-logs"
			idField="cursor"
			maxRows={2000}
			bind:control={logControl}
		/>
	</div>
</div>
