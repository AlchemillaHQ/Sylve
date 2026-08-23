<script lang="ts">
	import type { ZFSDashboardARCPoint } from '$lib/types/zfs/pool';
	import { formatBytesBinary } from '$lib/utils/bytes';

	interface Props {
		arc: ZFSDashboardARCPoint | null;
	}

	let { arc }: Props = $props();
	let compressionRatio = $derived(
		arc && arc.compressedSize > 0 ? arc.uncompressedSize / arc.compressedSize : 0
	);
	let compositionTotal = $derived(
		Math.max(
			arc?.size ?? 0,
			(arc?.dataSize ?? 0) +
				(arc?.metadataSize ?? 0) +
				(arc?.otherSize ?? 0) +
				(arc?.headerSize ?? 0)
		)
	);

	function segmentWidth(value: number): number {
		return compositionTotal > 0 ? Math.max(0, (value / compositionTotal) * 100) : 0;
	}

	function ratio(value: number | null): string {
		return value === null ? 'Warming up' : `${value.toFixed(1)}%`;
	}

	function targetWidth(value: number): number {
		if (!arc || arc.maxSize <= 0) return 0;
		return Math.min(100, (value / arc.maxSize) * 100);
	}
</script>

{#if arc}
	<div class="arc-grid">
		<section aria-labelledby="arc-hit-rate">
			<p
				id="arc-hit-rate"
				class="text-muted-foreground text-xs font-medium tracking-wide uppercase"
			>
				ARC hit rate
			</p>
			<p class="mt-2 text-3xl font-semibold tracking-tight tabular-nums">{ratio(arc.hitRatio)}</p>
			<div class="mt-4 grid grid-cols-2 gap-3 text-xs">
				<div>
					<p class="text-muted-foreground">Demand</p>
					<p class="mt-1 font-semibold tabular-nums">{ratio(arc.demandHitRatio)}</p>
				</div>
				<div>
					<p class="text-muted-foreground">Prefetch</p>
					<p class="mt-1 font-semibold tabular-nums">{ratio(arc.prefetchHitRatio)}</p>
				</div>
			</div>
		</section>

		<section class="arc-section" aria-labelledby="arc-size">
			<div class="flex items-center justify-between gap-3">
				<p id="arc-size" class="text-muted-foreground text-xs font-medium tracking-wide uppercase">
					ARC size
				</p>
				<p class="text-xs font-semibold tabular-nums">{formatBytesBinary(arc.size)}</p>
			</div>
			<div class="bg-muted relative mt-3 h-2 overflow-visible rounded-full">
				<div
					class="h-full rounded-full bg-emerald-500"
					style:width={`${targetWidth(arc.size)}%`}
				></div>
				<div
					class="border-foreground/60 absolute top-[-3px] h-3.5 border-l"
					style:left={`${targetWidth(arc.targetSize)}%`}
					title="Adaptive target"
				></div>
			</div>
			<div class="mt-3 grid grid-cols-2 gap-3 text-xs">
				<div>
					<p class="text-muted-foreground">Adaptive target</p>
					<p class="mt-1 font-medium tabular-nums">{formatBytesBinary(arc.targetSize)}</p>
				</div>
				<div>
					<p class="text-muted-foreground">Maximum target</p>
					<p class="mt-1 font-medium tabular-nums">{formatBytesBinary(arc.maxSize)}</p>
				</div>
				<div>
					<p class="text-muted-foreground">Minimum target</p>
					<p class="mt-1 font-medium tabular-nums">{formatBytesBinary(arc.minSize)}</p>
				</div>
				<div>
					<p class="text-muted-foreground">Memory compression</p>
					<p class="mt-1 font-medium tabular-nums">
						{compressionRatio > 0 ? `${compressionRatio.toFixed(2)}×` : '—'}
					</p>
				</div>
			</div>
		</section>

		<section class="arc-section" aria-labelledby="arc-composition">
			<p
				id="arc-composition"
				class="text-muted-foreground text-xs font-medium tracking-wide uppercase"
			>
				Memory composition
			</p>
			<div class="bg-muted mt-3 flex h-2 overflow-hidden rounded-full" aria-hidden="true">
				<div class="bg-blue-500" style:width={`${segmentWidth(arc.dataSize)}%`}></div>
				<div class="bg-violet-500" style:width={`${segmentWidth(arc.metadataSize)}%`}></div>
				<div
					class="bg-slate-400 dark:bg-slate-500"
					style:width={`${segmentWidth(arc.otherSize)}%`}
				></div>
				<div class="bg-amber-400" style:width={`${segmentWidth(arc.headerSize)}%`}></div>
			</div>
			<div class="mt-3 grid grid-cols-2 gap-x-3 gap-y-2 text-xs">
				<p>
					<span class="text-muted-foreground">Data</span>
					<span class="float-right tabular-nums">{formatBytesBinary(arc.dataSize)}</span>
				</p>
				<p>
					<span class="text-muted-foreground">Metadata</span>
					<span class="float-right tabular-nums">{formatBytesBinary(arc.metadataSize)}</span>
				</p>
				<p>
					<span class="text-muted-foreground">Other</span>
					<span class="float-right tabular-nums">{formatBytesBinary(arc.otherSize)}</span>
				</p>
				<p>
					<span class="text-muted-foreground">Headers</span>
					<span class="float-right tabular-nums">{formatBytesBinary(arc.headerSize)}</span>
				</p>
			</div>
		</section>
	</div>

	<div class="mt-4 grid gap-2 border-t pt-4 sm:grid-cols-3">
		<div class="bg-muted/40 rounded-md px-3 py-2 text-xs">
			<span class="text-muted-foreground">Evictions</span><span
				class="float-right font-medium tabular-nums">{arc.evictionsPerSecond.toFixed(1)}/s</span
			>
		</div>
		<div class="bg-muted/40 rounded-md px-3 py-2 text-xs">
			<span class="text-muted-foreground">Memory throttle</span><span
				class="float-right font-medium tabular-nums">{arc.memoryThrottleEvents}</span
			>
		</div>
		<div class="bg-muted/40 rounded-md px-3 py-2 text-xs">
			<span class="text-muted-foreground">Reclaim shortfalls</span><span
				class="float-right font-medium tabular-nums">{arc.evictNotEnoughEvents}</span
			>
		</div>
	</div>

	{#if arc.l2DeviceCount > 0}
		<div class="mt-2 grid gap-2 sm:grid-cols-3">
			<div class="bg-muted/40 rounded-md px-3 py-2 text-xs">
				<span class="text-muted-foreground">L2ARC size</span><span
					class="float-right font-medium tabular-nums">{formatBytesBinary(arc.l2Allocated)}</span
				>
			</div>
			<div class="bg-muted/40 rounded-md px-3 py-2 text-xs">
				<span class="text-muted-foreground">L2ARC hit rate</span><span
					class="float-right font-medium tabular-nums">{ratio(arc.l2HitRatio)}</span
				>
			</div>
			<div class="bg-muted/40 rounded-md px-3 py-2 text-xs">
				<span class="text-muted-foreground">L2ARC devices</span><span
					class="float-right font-medium tabular-nums">{arc.l2DeviceCount}</span
				>
			</div>
		</div>
	{/if}
{:else}
	<div
		class="text-muted-foreground flex min-h-48 flex-col items-center justify-center gap-2 text-center"
		role="status"
	>
		<span class="icon-[mdi--memory] size-6" aria-hidden="true"></span>
		<p class="text-sm font-medium">ARC telemetry is warming up</p>
		<p class="text-xs">The first cache sample appears after the collector starts.</p>
	</div>
{/if}

<style>
	.arc-grid {
		display: grid;
		gap: 1rem;
	}

	.arc-section {
		border-top: 1px solid var(--border);
		padding-top: 1rem;
	}

	@media (min-width: 760px) {
		.arc-grid {
			grid-template-columns: 0.75fr 1.15fr 1.4fr;
		}

		.arc-section {
			border-top: 0;
			border-left: 1px solid var(--border);
			padding-top: 0;
			padding-left: 1rem;
		}
	}
</style>
