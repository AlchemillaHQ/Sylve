<script lang="ts">
	import { State } from 'svelte-ux';
	import {
		Area,
		Axis,
		Chart,
		ChartClipPath,
		Layer,
		LinearGradient,
		Tooltip,
		Points
	} from 'layerchart';
	import { curveBasis } from 'd3-shape';
	import * as Card from '$lib/components/ui/card/index.js';

	interface Props {
		points: { date: Date; value: number }[];
		maxY?: number;
		label?: string;
		showPoints: boolean;
		icon?: string;
		containerClass?: string;
		description?: string;
	}

	let {
		points,
		maxY,
		label = 'Value',
		showPoints = false,
		icon = '',
		containerClass = 'p-5',
		description = ''
	}: Props = $props();
</script>

<Card.Root class={containerClass}>
	<Card.Header class="p-0">
		<Card.Title class="flex items-center justify-between gap-4">
			<div class="flex items-center gap-2">
				{#if icon}
					<span class={icon}></span>
				{/if}
				{label}
			</div>
		</Card.Title>
		{#if description}
			<Card.Description>{description}</Card.Description>
		{/if}
	</Card.Header>

	<Card.Content class="h-full min-h-[300px] w-full p-0">
		<div class="grid gap-1 rounded-sm border p-4">
			<State initial={[null, null]} let:value={xDomain} let:set>
				<div class="h-[300px]">
					<Chart
						data={points}
						x="date"
						{xDomain}
						y="value"
						yDomain={[0, maxY ?? null]}
						padding={{ left: 16, bottom: 24 }}
						brush={{
							resetOnEnd: true,
							onBrushEnd: (e) => {
								// @ts-expect-error
								set(e.xDomain);
							}
						}}
						tooltip={{ mode: 'quadtree-x' }}
					>
						<Layer type="svg">
							<Axis placement="left" grid rule />
							<Axis placement="bottom" rule />
							<ChartClipPath>
								<LinearGradient class="from-primary/50 to-primary/1" vertical>
									{#snippet children({ gradient })}
										<Area
											line={{ class: 'stroke-2 stroke-primary' }}
											fill={gradient}
											curve={curveBasis}
											motion={'tween'}
										/>
									{/snippet}
								</LinearGradient>
							</ChartClipPath>

							{#if showPoints}
								<Points motion={'tween'} r={3} />
							{/if}
						</Layer>

						<Tooltip.Root>
							{#snippet children({ data })}
								<Tooltip.Header value={data.date} format="daytime" />
								<Tooltip.List>
									<Tooltip.Item {label} value={Number(data.value).toFixed(2)} />
								</Tooltip.List>
							{/snippet}
						</Tooltip.Root>
					</Chart>
				</div>
			</State>
		</div>
	</Card.Content>
</Card.Root>
