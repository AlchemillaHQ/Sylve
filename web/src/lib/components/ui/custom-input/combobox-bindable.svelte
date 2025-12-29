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
		triggerWidth?: string;
		width?: string;
		labelExtraClasses?: string;
		disallowEmpty?: boolean;
		multiple?: boolean;
		showSelected?: boolean;
	}

	let {
		open = $bindable(false),
		label = '',
		data = $bindable([]),
		onValueChange = () => {},
		placeholder = '',
		disabled = false,
		classes = 'space-y-1',
		triggerWidth = 'w-full',
		labelExtraClasses = '',
		width = 'w-1/2',
		disallowEmpty = false,
		multiple = false,
		showSelected = true,
		value = $bindable(multiple ? [] : '')
	}: Props = $props();

	let search = $state('');

	const initialDataValues: string[] = $state(Array.isArray(data) ? data.map((d) => d.value) : []);

	const filteredData = $derived.by(() => {
		if (!search) return data;
		const q = search.toLowerCase();
		return data.filter(
			({ label, value }) => label.toLowerCase().includes(q) || value.toLowerCase().includes(q)
		);
	});

	function selectItem(val: string) {
		if (multiple) {
			const arr = Array.isArray(value) ? [...value] : [];
			const idx = arr.indexOf(val);
			if (idx >= 0) {
				arr.splice(idx, 1);
			} else {
				arr.push(val);
			}
			value = arr;
			onValueChange(arr);
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
		<Label class="w-full whitespace-nowrap text-sm {labelExtraClasses}" for={label.toLowerCase()}>
			{label}
		</Label>
	{/if}
	<Popover.Root bind:open>
		<Popover.Trigger class={triggerWidth} {disabled}>
			<Button
				variant="outline"
				role="combobox"
				aria-expanded={open}
				class=" h-full max-h-28 w-full flex-wrap justify-start gap-1 overflow-y-auto"
				{disabled}
			>
				<!-- <div class="flex min-w-0 flex-1 items-center gap-1 overflow-hidden"> -->
				{#if selectedLabels.length > 0}
					{#if showSelected}
						{#each selectedLabels as lbl, i}
							<span
								class={multiple
									? 'bg-secondary truncate rounded px-2 py-0.5 text-sm'
									: 'truncate rounded px-2 text-sm'}
								title={lbl}
							>
								{lbl}
							</span>
						{/each}
					{:else}
						<span class="truncate rounded px-2 text-sm">
							{selectedLabels.length} selected
						</span>
					{/if}
				{:else}
					<span class="truncate opacity-50">{placeholder}</span>
				{/if}
				<span class="icon-[lucide--chevrons-up-down] ml-auto h-4 w-4 shrink-0 opacity-50"></span>
			</Button>
		</Popover.Trigger>

		<Popover.Content class="{width} p-0! mx-auto overflow-x-auto overflow-y-auto whitespace-nowrap">
			<Command.Root shouldFilter={false}>
				<Command.Input bind:value={search} placeholder={placeholder || 'Search...'} />
				<Command.Empty>Type to create</Command.Empty>
				<div class="max-h-64 overflow-auto">
					<Command.Group>
						{#each filteredData as element}
							<Command.Item
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
								<p class="truncate">
									{element.label}
								</p>
								{#if !initialDataValues.includes(element.value)}
									<Button
										size="icon"
										onclick={() => {
											data = data.filter((d) => d.value !== element.value);
											if (multiple) {
												if (Array.isArray(value)) {
													const arr = value.filter((v) => v !== element.value);
													value = arr;
													onValueChange(arr);
												}
											}
										}}
										variant="ghost"
										class="hover:bg-muted! m-0! ml-auto! h-5! w-5! p-1!"
									>
										<span
											class="icon-[material-symbols--delete-outline] text-foreground ml-auto h-4 w-4 transition-colors duration-200"
										></span>
									</Button>
								{/if}
							</Command.Item>
						{/each}
						{#if filteredData.length === 0 && search.trim() !== ''}
							<Command.Item
								class="flex items-center  "
								onSelect={() => {
									const newOption = { value: search.trim(), label: search.trim() };
									if (multiple) {
										const arr = Array.isArray(value) ? [...value] : [];
										arr.push(newOption.value);
										value = arr;
									}
									data = [...data, newOption];
									onValueChange?.(value);
									search = '';
								}}
							>
								<span class="icon-[lucide--plus] mr-2 h-4 w-4 opacity-100"></span>

								<span>Add</span>
								<p class="wrap-break-word w-full text-left text-sm">
									"{search}"
								</p>
							</Command.Item>
						{/if}
					</Command.Group>
				</div>
			</Command.Root>
		</Popover.Content>
	</Popover.Root>
</div>
