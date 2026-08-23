<script lang="ts">
	import * as Card from '$lib/components/ui/card/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';

	type Accent = 'blue' | 'emerald' | 'violet' | 'amber' | 'red';

	interface Props {
		title: string;
		value: string;
		detail: string;
		iconClass: string;
		accent?: Accent;
		progress?: number | null;
		footerLabel?: string;
		footerValue?: string;
	}

	let {
		title,
		value,
		detail,
		iconClass,
		accent = 'blue',
		progress = null,
		footerLabel = '',
		footerValue = ''
	}: Props = $props();

	const accentClasses: Record<Accent, { line: string; icon: string; glow: string }> = {
		blue: {
			line: 'bg-blue-500',
			icon: 'bg-blue-500/10 text-blue-600 dark:text-blue-400',
			glow: 'from-blue-500/8'
		},
		emerald: {
			line: 'bg-emerald-500',
			icon: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
			glow: 'from-emerald-500/8'
		},
		violet: {
			line: 'bg-violet-500',
			icon: 'bg-violet-500/10 text-violet-600 dark:text-violet-400',
			glow: 'from-violet-500/8'
		},
		amber: {
			line: 'bg-amber-500',
			icon: 'bg-amber-500/10 text-amber-600 dark:text-amber-400',
			glow: 'from-amber-500/8'
		},
		red: {
			line: 'bg-red-500',
			icon: 'bg-red-500/10 text-red-600 dark:text-red-400',
			glow: 'from-red-500/8'
		}
	};

	let palette = $derived(accentClasses[accent]);
</script>

<Card.Root class="relative h-full min-h-40 gap-0 overflow-hidden py-0 shadow-sm">
	<div class={['absolute inset-x-0 top-0 h-0.5', palette.line]}></div>
	<div
		class={[
			'pointer-events-none absolute inset-0 bg-linear-to-br to-transparent opacity-70',
			palette.glow
		]}
	></div>
	<Card.Content class="relative flex h-full flex-col p-5">
		<div class="flex items-center justify-between gap-3">
			<p class="text-muted-foreground text-sm font-medium">{title}</p>
			<div class={['grid size-8 shrink-0 place-items-center rounded-lg', palette.icon]}>
				<span class={[iconClass, 'size-4']}></span>
			</div>
		</div>

		<div class="mt-4 min-w-0">
			<div class="truncate text-2xl font-semibold tracking-tight tabular-nums">{value}</div>
			<p class="text-muted-foreground mt-1 truncate text-xs">{detail}</p>
		</div>

		<div class="mt-auto pt-4">
			{#if progress !== null}
				<Progress
					value={Math.max(0, Math.min(progress, 100))}
					max={100}
					class="h-1.5"
					progressClass={palette.line}
				/>
			{:else}
				<div class="bg-border/70 h-px"></div>
			{/if}
			{#if footerLabel || footerValue}
				<div class="text-muted-foreground mt-2 flex items-center justify-between gap-3 text-[11px]">
					<span class="truncate">{footerLabel}</span>
					<span class="text-foreground shrink-0 font-medium tabular-nums">{footerValue}</span>
				</div>
			{/if}
		</div>
	</Card.Content>
</Card.Root>
