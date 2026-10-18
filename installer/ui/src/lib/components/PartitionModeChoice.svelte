<script lang="ts">
	import { cn } from '$lib/utils';
	import type { PartitioningMode } from '$lib/pool';
	import Database from '@lucide/svelte/icons/database';
	import HardDrive from '@lucide/svelte/icons/hard-drive';

	let {
		value = $bindable('zfs' as PartitioningMode),
		onSelect
	}: { value: PartitioningMode; onSelect?: (mode: PartitioningMode) => void } = $props();

	const modes: {
		id: PartitioningMode;
		label: string;
		desc: string;
		badge?: string;
		icon: typeof Database;
	}[] = [
		{
			id: 'zfs',
			label: 'Auto (ZFS)',
			desc: 'ZFS root pool with datasets, snapshots, RAID layouts and GELI.',
			badge: 'recommended',
			icon: Database
		},
		{
			id: 'ufs',
			label: 'Auto (UFS)',
			desc: 'Guided UFS root — simple and light. Sylve runs on UFS too.',
			icon: HardDrive
		},
	];
</script>

<div class="grid gap-3 sm:grid-cols-2">
	{#each modes as mode (mode.id)}
		<button
			class={cn(
				'flex h-full flex-col items-start gap-2 rounded-lg border p-3 text-left transition-colors',
				value === mode.id ? 'border-primary bg-primary/5' : 'border-border hover:bg-muted/40'
			)}
			onclick={() => {
				value = mode.id;
				onSelect?.(mode.id);
			}}
			aria-pressed={value === mode.id}
		>
			<span class="flex w-full items-center gap-2">
				<mode.icon class="size-4 shrink-0" />
				<span class="text-sm font-medium">{mode.label}</span>
				{#if mode.badge}
					<span
						class="ml-auto rounded-full border border-emerald-600/40 bg-emerald-600/10 px-2 py-0.5 text-[10px] font-semibold tracking-wide text-emerald-700 uppercase dark:border-emerald-400/40 dark:bg-emerald-400/10 dark:text-emerald-300"
					>
						{mode.badge}
					</span>
				{/if}
			</span>
			<span class="text-muted-foreground text-xs leading-snug">{mode.desc}</span>
		</button>
	{/each}
</div>
