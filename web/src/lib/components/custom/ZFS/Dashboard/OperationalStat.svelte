<script lang="ts">
	import * as Card from '$lib/components/ui/card/index.js';

	type Tone = 'neutral' | 'success' | 'warning' | 'danger';

	interface Props {
		label: string;
		value: string;
		detail: string;
		footer: string;
		iconClass: string;
		tone?: Tone;
		progress?: number | null;
	}

	let {
		label,
		value,
		detail,
		footer,
		iconClass,
		tone = 'neutral',
		progress = null
	}: Props = $props();

	const tones: Record<Tone, string> = {
		neutral: 'text-muted-foreground',
		success: 'text-emerald-600 dark:text-emerald-400',
		warning: 'text-amber-600 dark:text-amber-400',
		danger: 'text-red-600 dark:text-red-400'
	};
</script>

<Card.Root class="h-full gap-0 py-0 shadow-none">
	<Card.Content class="flex h-full min-h-32 flex-col p-4">
		<div class="flex items-center justify-between gap-3">
			<h2 class="text-muted-foreground text-xs font-medium tracking-wide uppercase">{label}</h2>
			<span class={[iconClass, 'size-4', tones[tone]]} aria-hidden="true"></span>
		</div>
		<div class="mt-3 truncate text-xl font-semibold tracking-tight tabular-nums">{value}</div>
		<p class="text-muted-foreground mt-1 min-h-5 truncate text-xs">{detail}</p>
		<div class="mt-auto pt-3">
			{#if progress !== null}
				<div
					class="bg-muted h-1.5 overflow-hidden rounded-full"
					role="progressbar"
					aria-label={label}
					aria-valuemin="0"
					aria-valuemax="100"
					aria-valuenow={Math.max(0, Math.min(progress, 100))}
				>
					<div
						class={[
							'h-full rounded-full',
							tone === 'danger' ? 'bg-red-500' : tone === 'warning' ? 'bg-amber-500' : 'bg-blue-500'
						]}
						style:width={`${Math.max(0, Math.min(progress, 100))}%`}
					></div>
				</div>
			{:else}
				<div class="bg-border h-px"></div>
			{/if}
			<p class="text-muted-foreground mt-2 truncate text-xs">{footer}</p>
		</div>
	</Card.Content>
</Card.Root>
