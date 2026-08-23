<script lang="ts">
	import { Badge } from '$lib/components/ui/badge/index.js';
	import type { ZFSDashboardPoolSnapshot } from '$lib/types/zfs/pool';
	import { formatBytesBinary } from '$lib/utils/bytes';

	interface Props {
		pools: ZFSDashboardPoolSnapshot[];
		selectedGUID: string;
		onSelect: (guid: string) => void;
	}

	let { pools, selectedGUID, onSelect }: Props = $props();

	function usedPercent(pool: ZFSDashboardPoolSnapshot): number {
		return pool.size > 0 ? (pool.allocated / pool.size) * 100 : 0;
	}

	function errorCount(pool: ZFSDashboardPoolSnapshot): number {
		return pool.errors.read + pool.errors.write + pool.errors.checksum + pool.errors.scan;
	}

	function stateClass(state: string): string {
		switch (state.toUpperCase()) {
			case 'ONLINE':
				return 'border-emerald-500/30 text-emerald-600 dark:text-emerald-400';
			case 'DEGRADED':
				return 'border-amber-500/30 text-amber-600 dark:text-amber-400';
			default:
				return 'border-red-500/30 text-red-600 dark:text-red-400';
		}
	}

	function parseScanTime(value: string): Date | null {
		if (!value) return null;
		if (/^\d+$/.test(value)) return new Date(Number(value) * 1000);
		const parsed = new Date(value);
		return Number.isNaN(parsed.getTime()) ? null : parsed;
	}

	function scanLabel(pool: ZFSDashboardPoolSnapshot): string {
		const scan = pool.scan;
		if (!scan) return 'No scrub recorded';
		const name = scan.function.toLowerCase() === 'resilver' ? 'Resilver' : 'Scrub';
		const state = scan.state.toUpperCase();
		if (state === 'SCANNING' || state === 'PAUSED') {
			return `${name}${state === 'PAUSED' ? ' paused' : ' active'}${scan.progressPercent === null ? '' : ` · ${scan.progressPercent.toFixed(0)}%`}`;
		}
		if (state === 'CANCELED' || state === 'CANCELLED' || state === 'INTERRUPTED')
			return `${name} incomplete`;
		if (scan.errors > 0) return `${name} found ${scan.errors} error${scan.errors === 1 ? '' : 's'}`;
		const completed = parseScanTime(scan.endTime || scan.startTime);
		return completed
			? `Last ${name.toLowerCase()} clean · ${completed.toLocaleDateString([], { month: 'short', day: 'numeric' })}`
			: `${name} complete`;
	}
</script>

<div class="pool-list max-h-72 overflow-y-auto border-y">
	{#each pools as pool (pool.guid)}
		<button
			type="button"
			class={[
				'pool-row hover:bg-muted/45 focus-visible:ring-ring block w-full px-3 py-3 text-left transition-colors focus-visible:ring-2 focus-visible:outline-none',
				selectedGUID === pool.guid && 'bg-muted/60'
			]}
			onclick={() => onSelect(pool.guid)}
			aria-pressed={selectedGUID === pool.guid}
		>
			<div class="flex min-w-0 items-start justify-between gap-4">
				<div class="flex min-w-0 items-center gap-2">
					<span class="truncate text-sm font-semibold">{pool.name}</span>
					<Badge variant="outline" class={['shrink-0 text-[10px]', stateClass(pool.state)]}
						>{pool.state}</Badge
					>
				</div>
				<div class="flex shrink-0 items-center gap-2 text-xs">
					<span class="text-muted-foreground">Used</span>
					<span class="font-semibold tabular-nums">{usedPercent(pool).toFixed(0)}%</span>
				</div>
			</div>

			<div
				class="bg-muted mt-2 h-1.5 overflow-hidden rounded-full"
				role="progressbar"
				aria-label={`${pool.name} capacity used`}
				aria-valuemin="0"
				aria-valuemax="100"
				aria-valuenow={Math.min(100, usedPercent(pool))}
			>
				<div
					class={[
						'h-full rounded-full',
						usedPercent(pool) >= 90
							? 'bg-red-500'
							: usedPercent(pool) >= 80
								? 'bg-amber-500'
								: 'bg-blue-500'
					]}
					style:width={`${Math.min(100, usedPercent(pool))}%`}
				></div>
			</div>

			<div class="text-muted-foreground mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs">
				<span>{formatBytesBinary(pool.free)} free</span>
				<span class={errorCount(pool) > 0 ? 'font-medium text-red-600 dark:text-red-400' : ''}>
					{errorCount(pool) > 0
						? `${errorCount(pool)} errors`
						: pool.statusAvailable
							? 'No known errors'
							: 'Status unavailable'}
				</span>
				<span>{scanLabel(pool)}</span>
			</div>
		</button>
	{/each}
</div>

<style>
	.pool-row + .pool-row {
		border-top: 1px solid var(--border);
	}
</style>
