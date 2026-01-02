<script lang="ts">
	import { Arc, ArcChart, Group, LinearGradient, Text } from 'layerchart';

	interface Props {
		title: string;
		subtitle?: string;
		value: number;
	}

	let { title, subtitle, value }: Props = $props();

	const ranges = [
		{ max: 33, color: 'fill-green-500' },
		{ max: 66, color: 'fill-yellow-500' },
		{ max: 100, color: 'fill-red-500' }
	] as const;

	const color = $derived(ranges.find((r) => value <= r.max)?.color ?? 'fill-red-500');
</script>

<div class="h-[150px] w-[200px] overflow-auto rounded-sm">
	<ArcChart>
		{#snippet marks()}
			<Group y={20}>
				<Arc
					{value}
					domain={[0, 100]}
					outerRadius={80}
					innerRadius={-15}
					cornerRadius={20}
					padAngle={0.02}
					range={[-120, 120]}
					class={`${color} opacity-70`}
					track={{ class: 'fill-none stroke-primary/20' }}
				>
					{#snippet children({ value })}
						<Text
							value={title}
							textAnchor="middle"
							verticalAnchor="end"
							y={-35}
							class="text-md font-bold"
							fill="currentColor"
						/>

						{#if subtitle}
							<Text
								value={subtitle}
								textAnchor="middle"
								verticalAnchor="end"
								y={-10}
								class="text-xs font-medium"
								fill="currentColor"
							/>
						{/if}

						<Text
							value={Math.round(value) + '%'}
							textAnchor="middle"
							verticalAnchor="start"
							y={10}
							class="text-3xl font-bold"
							fill="currentColor"
						/>
					{/snippet}
				</Arc>
			</Group>
		{/snippet}
	</ArcChart>
</div>
