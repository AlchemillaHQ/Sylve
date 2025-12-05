<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Command from '$lib/components/ui/command/index.js';
	import Label from '$lib/components/ui/label/label.svelte';
	import * as Popover from '$lib/components/ui/popover/index.js';
	import { cn } from '$lib/utils.js';

	interface Props {
		open: boolean;
		label?: string;
		value: string | string[];
		data: { value: string; label: string }[];
		onValueChange?: (value: string | string[]) => void;
		placeholder?: string;
		disabled?: boolean;
		classes?: string;
		commandClasses?: string;
		triggerWidth?: string;
		width?: string;
		disallowEmpty?: boolean;
		multiple?: boolean;
	}

	let {
		open = $bindable(false),
		label = '',
		data = [],
		onValueChange = () => {},
		placeholder = '',
		disabled = false,
		classes = 'space-y-1',
		commandClasses = '',
		triggerWidth = 'w-full',
		width = 'w-1/2',
		disallowEmpty = false,
		multiple = false,
		value = $bindable(multiple ? [] : '')
	}: Props = $props();

	let search = $state('');

	const filteredData = $derived.by(() => {
		if (!search) return data;
		const q = search.toLowerCase();
		return data.filter(
			({ label, value }) => label.toLowerCase().includes(q) || value.toLowerCase().includes(q)
		);
	});

	function selectItem(val: string) {
		if (multiple) {
			// start with a fresh array copy
			const arr = Array.isArray(value) ? [...value] : [];
			const idx = arr.indexOf(val);
			if (idx >= 0) {
				arr.splice(idx, 1);
			} else {
				arr.push(val);
			}
			value = arr;
			onValueChange(arr);
			// keep open=true so you can pick more
		} else {
			if (value === val && !disallowEmpty) {
				value = '';
				onValueChange('');
			} else {
				value = val;
				onValueChange(val);
			}
			open = false;
		}
		search = '';
	}

	const selectedLabels = $derived.by(() => {
		const vals = multiple ? (Array.isArray(value) ? value : []) : value ? [value] : [];

		return data.filter((d) => vals.includes(d.value)).map((d) => d.label);
	});
</script>

<div class={classes}>
	{#if label}
		<Label class="w-full whitespace-nowrap text-sm" for={label.toLowerCase()}>
			{label}
		</Label>
	{/if}
	<Popover.Root bind:open>
		<Popover.Trigger class={triggerWidth} {disabled}>
			<Button
				variant="outline"
				role="combobox"
				aria-expanded={open}
				class="max-h-40! h-full w-full flex-nowrap justify-between gap-1 overflow-y-auto"
				{disabled}
			>
				<div class="flex min-w-0 flex-1 flex-wrap items-center gap-1 overflow-hidden">
					{#if selectedLabels.length > 0}
						{#each selectedLabels as lbl, i}
							<p
								class={multiple
									? 'bg-secondary = max-w-full whitespace-break-spaces rounded px-2 text-left text-sm'
									: ' rounded px-2 text-sm'}
								title={lbl}
							>
								{lbl}
							</p>
						{/each}
					{:else}
						<span class="truncate opacity-50">{placeholder}</span>
					{/if}
				</div>

				<span class="icon-[lucide--chevrons-up-down] ml-auto h-4 w-4 shrink-0 opacity-50"></span>
			</Button>
		</Popover.Trigger>

		<Popover.Content class="{width} mx-auto p-0">
			<Command.Root shouldFilter={false}>
				<Command.Input bind:value={search} placeholder={placeholder || 'Search...'} />
				<Command.Empty>No data</Command.Empty>
				<div class="max-h-64 overflow-y-auto">
					<Command.Group>
						{#each filteredData as element}
							<Command.Item
								class={commandClasses}
								value={element.value}
								onSelect={() => selectItem(element.value)}
								onkeydown={(e) => {
									if (e.key === 'Enter') selectItem(element.value);
								}}
							>
								<span
									class={`icon-[lucide--check] ${cn(
										'mr-2 h-4 w-4',
										multiple
											? Array.isArray(value) && value.includes(element.value)
												? 'opacity-100'
												: 'opacity-0'
											: value === element.value
												? 'opacity-100'
												: 'opacity-0'
									)}`}
								></span>
								{element.label}
							</Command.Item>
						{/each}
					</Command.Group>
				</div>
			</Command.Root>
		</Popover.Content>
	</Popover.Root>
</div>
