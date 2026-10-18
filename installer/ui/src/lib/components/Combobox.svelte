<script lang="ts">
	import * as Popover from '$lib/components/ui/popover';
	import * as Command from '$lib/components/ui/command';
	import { cn } from '$lib/utils';
	import Check from '@lucide/svelte/icons/check';
	import ChevronsUpDown from '@lucide/svelte/icons/chevrons-up-down';
	import Pencil from '@lucide/svelte/icons/pencil';

	export type Option = { value: string; label: string };

	let {
		value = $bindable(''),
		id,
		options,
		placeholder = 'Select…',
		searchPlaceholder = 'Search…',
		emptyText = 'No match.',
		allowCustom = false,
		class: className
	}: {
		value?: string;
		id?: string;
		options: Option[];
		placeholder?: string;
		searchPlaceholder?: string;
		emptyText?: string;
		allowCustom?: boolean;
		class?: string;
	} = $props();

	let open = $state(false);
	let query = $state('');
	const current = $derived(options.find((o) => o.value === value)?.label ?? value);
	const custom = $derived(
		query.trim() !== '' &&
			!options.some((o) => o.value === query.trim() || o.label === query.trim())
	);

	function select(next: string) {
		value = next;
		open = false;
	}
</script>

<Popover.Root
	bind:open
	onOpenChange={(value) => {
		if (!value) query = '';
	}}
>
	<Popover.Trigger
		{id}
		class={cn(
			'border-input bg-background shadow-xs hover:bg-muted dark:bg-input/30 dark:hover:bg-muted inline-flex h-9 w-full items-center justify-between gap-2 rounded-md border px-3 text-sm transition-colors',
			className
		)}
		role="combobox"
		aria-expanded={open}
	>
		<span class={cn('truncate', !value && 'text-muted-foreground')}>{current || placeholder}</span>
		<ChevronsUpDown class="size-4 shrink-0 opacity-50" />
	</Popover.Trigger>
	<Popover.Content
		align="start"
		class="w-[var(--bits-popover-anchor-width)] min-w-72 p-0"
		onCloseAutoFocus={(event: Event) => event.preventDefault()}
	>
		<Command.Root>
			<Command.Input bind:value={query} placeholder={searchPlaceholder} />
			<Command.List>
				<Command.Empty>{emptyText}</Command.Empty>
				{#if allowCustom && custom}
					<Command.Item value={query.trim()} onSelect={() => select(query.trim())}>
						<Pencil class="mr-2 size-4" />
						<span class="truncate">Use “{query.trim()}”</span>
					</Command.Item>
				{/if}
				<Command.Group>
					{#each options as option (option.value)}
						<Command.Item value={option.value} onSelect={() => select(option.value)}>
							<Check
								class={cn('mr-2 size-4', value === option.value ? 'opacity-100' : 'opacity-0')}
							/>
							<span class="truncate">{option.label}</span>
						</Command.Item>
					{/each}
				</Command.Group>
			</Command.List>
		</Command.Root>
	</Popover.Content>
</Popover.Root>
