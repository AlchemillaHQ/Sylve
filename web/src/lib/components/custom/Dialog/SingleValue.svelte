<script lang="ts">
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { watch } from 'runed';

	type Option = { label: string; value: string };

	type BaseProps = {
		open: boolean;
		title: string;
		icon?: string;
		placeholder?: string;
		onSave: () => void;
		options?: Option[];
		loading?: boolean;
	};

	type TextProps = BaseProps & {
		type: 'text' | 'number';
		value: string;
	};

	type SelectProps = BaseProps & {
		type: 'select';
		value: string;
		options: Option[];
	};

	type ComboProps = BaseProps & {
		type: 'combobox';
		value: string;
		options: Option[];
	};

	type CheckboxProps = BaseProps & {
		type: 'checkbox';
		value: boolean;
	};

	type Props = TextProps | SelectProps | ComboProps | CheckboxProps;

	let {
		open = $bindable(),
		title,
		type,
		placeholder = '',
		icon = 'mdi--pencil',
		value = $bindable(),
		options = [],
		onSave,
		loading = $bindable(undefined)
	}: Props = $props();

	let stringValue = $state(typeof value === 'string' ? value : '');
	let boolValue = $state(typeof value === 'boolean' ? value : false);
	let comboValue = $state(
		typeof value === 'string'
			? value
					.split(',')
					.map((v) => v.trim())
					.filter(Boolean)
			: []
	);

	watch([() => value, () => type], ([v, t]) => {
		if (t === 'checkbox') {
			boolValue = typeof v === 'boolean' ? v : false;
		} else if (t === 'combobox') {
			const s = typeof v === 'string' ? v : '';
			comboValue = s
				.split(',')
				.map((x) => x.trim())
				.filter(Boolean);
			stringValue = s;
		} else {
			stringValue = typeof v === 'string' ? v : '';
		}
	});

	watch([() => stringValue, () => boolValue, () => comboValue, () => type], ([sv, bv, cv, t]) => {
		if (t === 'checkbox') value = bv;
		else if (t === 'combobox') value = cv.join(',');
		else value = sv;
	});

	// svelte-ignore state_referenced_locally
	let comboBox = $state({
		open: false,
		data: options.map((o) => ({ value: o.value, label: o.label })),
		placeholder,
		disabled: false,
		disallowEmpty: true,
		multiple: true
	});

	watch(
		[() => options, () => placeholder],
		([currOpts, currPlaceholder], [oldOpts, oldPlaceholder]) => {
			const optsChanged =
				currOpts.length !== oldOpts?.length ||
				currOpts.some((o, i) => o.value !== oldOpts?.[i]?.value || o.label !== oldOpts?.[i]?.label);

			const placeholderChanged = currPlaceholder !== oldPlaceholder;

			if (optsChanged) {
				comboBox.data = currOpts.map((o) => ({ value: o.value, label: o.label }));
			}

			if (placeholderChanged) {
				comboBox.placeholder = currPlaceholder;
			}
		}
	);
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="flex flex-col p-5" onInteractOutside={() => (open = false)}>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class={`icon-[${icon}]`} style="width: 24px; height: 24px;"></span>
					<span>{title}</span>
				</div>
				<div class="flex items-center gap-0.5">
					<Button size="sm" variant="link" class="h-4" title="Close" onclick={() => (open = false)}>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		{#if type === 'text' || type === 'number'}
			<CustomValueInput
				placeholder={placeholder || 'Enter value'}
				bind:value={stringValue}
				classes="flex-1 space-y-1.5"
			/>
		{/if}

		{#if type === 'select'}
			<SimpleSelect
				placeholder={placeholder || 'Select an option'}
				{options}
				bind:value={stringValue}
				onChange={(v) => (stringValue = v)}
			/>
		{/if}

		{#if type === 'combobox'}
			<CustomComboBox
				bind:open={comboBox.open}
				bind:value={comboValue}
				data={comboBox.data}
				onValueChange={(val) => (comboValue = Array.isArray(val) ? val : [val])}
				placeholder={comboBox.placeholder}
				disabled={comboBox.disabled}
				disallowEmpty={comboBox.disallowEmpty}
				multiple={comboBox.multiple}
				width="w-full"
			/>
		{/if}

		{#if type === 'checkbox'}
			<div class="mt-4">
				<CustomCheckbox label={placeholder || 'Check to enable'} bind:checked={boolValue} />
			</div>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button
					onclick={() => onSave()}
					type="submit"
					size="sm"
					disabled={loading !== undefined && loading}
				>
					{#if loading !== undefined}
						{#if loading}
							<div class="flex items-center gap-2">
								<span class="icon-[mdi--loading] animate-spin h-4 w-4"></span>
								<span>Saving</span>
							</div>
						{:else}
							<span>Save</span>
						{/if}
					{:else}
						Save
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
