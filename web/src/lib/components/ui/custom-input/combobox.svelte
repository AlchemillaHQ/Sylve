<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Command from '$lib/components/ui/command/index.js';
	import Label from '$lib/components/ui/label/label.svelte';
	import * as Popover from '$lib/components/ui/popover/index.js';
	import { cn } from '$lib/utils.js';
	import { watch } from 'runed';

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
		allowCustom?: boolean;
		shortLabels?: boolean;
		showCount?: boolean;
		showCountLabel?: string;
		buttonClass?: string;
		topRightButton?: {
			icon: string;
			tooltip: string;
			function: () => Promise<string>;
		};
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
		allowCustom = false,
		value = $bindable(multiple ? [] : ''),
		shortLabels = false,
		topRightButton,
		showCount = false,
		showCountLabel = 'selected',
		buttonClass = 'h-9'
	}: Props = $props();

	let search = $state('');

	watch(
		() => open,
		(val) => {
			if (val) search = '';
		}
	);

	const effectiveData = $derived.by(() => {
		if (allowCustom) {
			if (!multiple && typeof value === 'string' && value && !data.some((d) => d.value === value)) {
				return [{ value: value, label: value }, ...data];
			}
			if (multiple && Array.isArray(value)) {
				const extras = value
					.filter((v) => v && !data.some((d) => d.value === v))
					.map((v) => ({ value: v, label: v }));
				return extras.length > 0 ? [...extras, ...data] : data;
			}
		}
		return data;
	});

	const filteredData = $derived.by(() => {
		if (!search) return effectiveData;
		const q = search.toLowerCase();
		return effectiveData.filter(
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
	}

	const selectedLabels = $derived.by(() => {
		const vals = multiple ? (Array.isArray(value) ? value : []) : value ? [value] : [];

		const matched = effectiveData.filter((d) => vals.includes(d.value)).map((d) => d.label);
		return matched;
	});

	function formatLabel(lbl: string): string {
		if (!shortLabels || lbl.length <= 32) return lbl;
		return `${lbl.slice(0, 32)}…${lbl.slice(-8)}`;
	}
</script>

<div class="{classes} min-w-0 overflow-hidden">
	{#if label}
		{#if topRightButton}
			<div class="flex h-7 items-center justify-between w-full">
				<Label class="whitespace-nowrap text-sm" for={label.toLowerCase()}>
					{label}
				</Label>
				<Button
					variant="outline"
					size="icon"
					class="h-6 w-6 shrink-0"
					title={topRightButton.tooltip}
					onclick={async () => {
						const result = await topRightButton.function();
						if (result) value = result;
					}}
				>
					<span class={`icon ${topRightButton.icon} w-4`}></span>
				</Button>
			</div>
		{:else}
			<Label class="whitespace-nowrap text-sm" for={label.toLowerCase()}>
				{label}
			</Label>
		{/if}
	{/if}
	<Popover.Root bind:open>
		<Popover.Trigger class="{triggerWidth} min-w-0" {disabled}>
			<Button
				variant="outline"
				role="combobox"
				aria-expanded={open}
				class="{buttonClass} w-full min-w-0 flex-nowrap justify-between gap-1 overflow-hidden"
				{disabled}
			>
				<div
					class="flex min-w-0 flex-1 items-center gap-1 overflow-hidden"
					class:flex-wrap={multiple && !showCount}
				>
					{#if selectedLabels.length > 0}
						{#if showCount && multiple}
							<p class="min-w-0 max-w-full truncate rounded px-2 text-sm">
								{selectedLabels.length}
								{showCountLabel}
							</p>
						{:else}
							{#each selectedLabels as lbl (lbl)}
								<p
									class={multiple
										? 'bg-secondary = max-w-full whitespace-break-spaces rounded px-2 text-left text-sm'
										: 'min-w-0 max-w-full truncate rounded px-2 text-sm'}
									title={lbl}
								>
									{lbl}
								</p>
							{/each}
						{/if}
					{:else}
						<span class="truncate opacity-50">{placeholder}</span>
					{/if}
				</div>

				<span class="icon-[lucide--chevrons-up-down] ml-auto h-4 w-4 shrink-0 opacity-50"></span>
			</Button>
		</Popover.Trigger>

		<Popover.Content class="{width} mx-auto p-0">
			<Command.Root shouldFilter={false}>
				<Command.Input
					bind:value={search}
					placeholder={placeholder || 'Search...'}
					onkeydown={(e) => {
						if (
							allowCustom &&
							e.key === 'Enter' &&
							search.trim() &&
							!data.some((d) => d.value === search.trim())
						) {
							e.preventDefault();
							selectItem(search.trim());
							if (!multiple) open = false;
						}
					}}
				/>
				<Command.Empty>No data</Command.Empty>
				<div class="max-h-64 overflow-y-auto">
					<Command.Group>
						{#each filteredData as element (element.label)}
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
								{formatLabel(element.label)}
							</Command.Item>
						{/each}
						{#if allowCustom && search.trim() && !data.some((d) => d.value === search.trim()) && !(multiple && Array.isArray(value) && value.includes(search.trim()))}
							<Command.Item
								value={search.trim()}
								onSelect={() => {
									selectItem(search.trim());
									if (!multiple) open = false;
								}}
							>
								<span class="icon-[lucide--check] mr-2 h-4 w-4 opacity-0"></span>
								Use "{search.trim()}"
							</Command.Item>
						{/if}
					</Command.Group>
				</div>
			</Command.Root>
		</Popover.Content>
	</Popover.Root>
</div>
