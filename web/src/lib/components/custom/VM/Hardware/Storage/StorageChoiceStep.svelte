<script lang="ts">
	import { intentDetails, primaryIntents, type StorageIntent } from './storage-dialog';

	interface Props {
		onSelect: (intent: StorageIntent) => void;
	}

	let { onSelect }: Props = $props();
</script>

<div class="w-full space-y-3">
	<div class="grid gap-2.5 sm:grid-cols-2">
		{#each primaryIntents as option (option)}
			{@const details = intentDetails[option]}
			<button
				type="button"
				class="group flex min-h-24 items-center gap-3.5 rounded-lg border bg-background p-4 text-left transition-colors hover:border-foreground/25 hover:bg-muted/30 focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] focus-visible:outline-none"
				onclick={() => onSelect(option)}
			>
				<span
					class="flex size-10 shrink-0 items-center justify-center rounded-lg border bg-muted/40 text-muted-foreground transition-colors group-hover:text-foreground"
				>
					<span class={details.icon + ' size-5'}></span>
				</span>
				<span class="min-w-0 flex-1">
					<span class="flex items-center justify-between gap-3">
						<span class="text-sm font-medium">{details.title}</span>
						<span
							class="icon-[lucide--chevron-right] size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5 motion-reduce:transform-none"
						></span>
					</span>
					<span class="mt-1 block text-sm leading-5 text-muted-foreground">
						{details.description}
					</span>
				</span>
			</button>
		{/each}
	</div>

	<button
		type="button"
		class="group flex w-full items-center gap-3 rounded-lg border bg-muted/20 px-4 py-3 text-left text-sm transition-colors hover:border-foreground/25 hover:bg-muted/40 focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] focus-visible:outline-none"
		onclick={() => onSelect('advanced')}
	>
		<span class="icon-[lucide--hard-drive-upload] size-4 shrink-0 text-muted-foreground"></span>
		<span class="min-w-0 flex-1">
			<span class="font-medium">Advanced host import</span>
			<span class="ml-2 text-muted-foreground">
				{intentDetails.advanced.description}
			</span>
		</span>
		<span
			class="icon-[lucide--chevron-right] size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5 motion-reduce:transform-none"
		></span>
	</button>
</div>
