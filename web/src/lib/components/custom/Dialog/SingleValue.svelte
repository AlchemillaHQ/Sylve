<script lang="ts">
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';

	interface Props {
		open: boolean;
		title: string;
		icon?: string;
		type: 'text' | 'number' | 'select' | 'combobox';
		placeholder?: string;
		value: string;
		options?: {
			label: string;
			value: string;
		}[];
		onSave: () => void;
	}

	let {
		open = $bindable(),
		title,
		type,
		placeholder = '',
		icon = 'mdi--pencil',
		value = $bindable(),
		options = [],
		onSave
	}: Props = $props();

	let comboBox = $state({
		open: false,
		value: value?.split(',').map((v) => v.trim()),
		data: options.map((o) => ({ value: o.value, label: o.label })),
		onValueChange: (val: string | string[]) => {
			if (Array.isArray(val)) {
				value = val.join(',');
			} else {
				value = val;
			}
		},
		placeholder: placeholder,
		disabled: false,
		disallowEmpty: true,
		multiple: true
	});
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex flex-col p-5"
		onInteractOutside={() => {
			open = false;
		}}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex  justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class={`icon-[${icon}]`} style="width: 24px; height: 24px;"></span>
					<span>{title}</span>
				</div>
				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		{#if type === 'text' || type === 'number'}
			<CustomValueInput
				placeholder={placeholder || 'Enter value'}
				bind:value
				classes="flex-1 space-y-1.5"
			/>
		{/if}

		{#if type === 'select'}
			<SimpleSelect
				placeholder={placeholder || 'Select an option'}
				{options}
				bind:value
				onChange={(v) => (value = v)}
			/>
		{/if}

		{#if type === 'combobox'}
			<CustomComboBox
				bind:open={comboBox.open}
				bind:value={comboBox.value}
				data={comboBox.data}
				onValueChange={comboBox.onValueChange}
				placeholder={comboBox.placeholder}
				disabled={comboBox.disabled}
				disallowEmpty={comboBox.disallowEmpty}
				multiple={comboBox.multiple}
				width="w-full"
			/>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={() => onSave()} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
