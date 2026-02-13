<script lang="ts">
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import {
		default as ComboBox,
		default as CustomComboBox
	} from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import Input from '$lib/components/ui/input/input.svelte';
	import Label from '$lib/components/ui/label/label.svelte';
	import { generatePassword } from '$lib/utils/string';
	import { cloudInitPlaceholders } from '$lib/utils/utilities/cloud-init';
	import { onMount } from 'svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SimpleSelect from '../../SimpleSelect.svelte';
	import { resource, watch } from 'runed';
	import { getTemplates } from '$lib/api/utilities/cloud-init';
	import type { CloudInitTemplate } from '$lib/types/utilities/cloud-init';
	import { resolutions } from '$lib/utils/vm/vnc';

	interface Props {
		serial: boolean;
		vncPort: number;
		vncPassword: string;
		vncWait: boolean;
		vncResolution: string;
		startAtBoot: boolean;
		bootOrder: number;
		tpmEmulation: boolean;
		timeOffset: 'utc' | 'localtime';
		cloudInit: {
			enabled: boolean;
			data: string;
			metadata: string;
			networkConfig: string;
		};
		ignoreUmsrs: boolean;
	}

	let {
		serial = $bindable(),
		vncPort = $bindable(),
		vncPassword = $bindable(),
		vncWait = $bindable(),
		vncResolution = $bindable(),
		startAtBoot = $bindable(),
		bootOrder = $bindable(),
		tpmEmulation = $bindable(),
		timeOffset = $bindable(),
		cloudInit = $bindable(),
		ignoreUmsrs = $bindable()
	}: Props = $props();

	onMount(() => {
		if (!vncPort) vncPort = Math.floor(Math.random() * (5999 - 5900 + 1)) + 5900;
	});

	let timeOffsetOpen = $state(false);
	const timeOffsets = [
		{ label: 'UTC', value: 'utc' },
		{ label: 'Local Time', value: 'localtime' }
	];

	let resolutionOpen = $state(false);

	watch(
		() => cloudInit.enabled,
		(enabled) => {
			if (!enabled) {
				cloudInit.data = '';
				cloudInit.metadata = '';
				cloudInit.networkConfig = '';
			}
		}
	);

	let templateSelector = $state({
		open: false,
		current: ''
	});

	let cloudInitTemplates = resource(
		() => 'cloud-init-templates',
		async (key, prevKey, { signal }) => {
			return await getTemplates();
		},
		{ initialValue: [] as CloudInitTemplate[] }
	);
</script>

<div class="flex flex-col gap-4 space-y-1.5 p-4">
	<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
		<CustomValueInput
			label="VNC Port"
			placeholder="5900"
			bind:value={vncPort}
			classes="flex-1 space-y-1.5"
		/>

		<div class="space-y-1.5">
			<Label class="w-24 whitespace-nowrap text-sm">VNC Password</Label>
			<div class="flex w-full max-w-sm items-center space-x-2">
				<Input
					type="password"
					id="d-passphrase"
					placeholder="Enter or generate passphrase"
					class="w-full"
					autocomplete="off"
					bind:value={vncPassword}
					showPasswordOnFocus={true}
				/>

				<Button
					onclick={() => {
						vncPassword = generatePassword();
					}}
				>
					<span class="icon-[fad--random-2dice] h-6 w-6"></span>
				</Button>
			</div>
		</div>
	</div>

	<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
		<CustomComboBox
			bind:open={resolutionOpen}
			label="VNC Resolution"
			bind:value={vncResolution}
			data={resolutions}
			classes="flex-1 space-y-1.5"
			placeholder="Select VNC resolution"
			triggerWidth="w-full "
			width="w-full"
		></CustomComboBox>

		<ComboBox
			bind:open={timeOffsetOpen}
			label="Clock Offset"
			bind:value={timeOffset}
			data={timeOffsets}
			classes="flex-1 space-y-1.5"
			placeholder="Select Time Offset"
			triggerWidth="w-full"
			width="w-full"
		></ComboBox>

		<CustomValueInput
			label="Startup/Shutdown Order"
			placeholder="0"
			type="number"
			bind:value={bootOrder}
			classes="flex-1 space-y-1.5"
		/>
	</div>

	<div class="mt-1 grid grid-cols-2 gap-4 lg:grid-cols-3">
		<CustomCheckbox label="Serial Console" bind:checked={serial} classes="flex items-center gap-2"
		></CustomCheckbox>

		<CustomCheckbox label="VNC Wait" bind:checked={vncWait} classes="flex items-center gap-2"
		></CustomCheckbox>

		<CustomCheckbox
			label="Start On Boot"
			bind:checked={startAtBoot}
			classes="flex items-center gap-2"
		></CustomCheckbox>

		<CustomCheckbox
			label="TPM Emulation"
			bind:checked={tpmEmulation}
			classes="flex items-center gap-2"
		></CustomCheckbox>

		<CustomCheckbox
			label="Enable Cloud-Init"
			bind:checked={cloudInit.enabled}
			classes="flex items-center gap-2"
		></CustomCheckbox>

		<CustomCheckbox
			label="Ignore Unimplemented MSR Accesses"
			bind:checked={ignoreUmsrs}
			classes="flex items-center gap-2 mt-2"
		></CustomCheckbox>
	</div>

	{#if cloudInit.enabled}
		<CustomValueInput
			label="Cloud-Init User Data"
			placeholder={cloudInitPlaceholders.data}
			bind:value={cloudInit.data}
			classes="flex-1 space-y-1.5"
			type="textarea"
			topRightButton={{
				icon: 'icon-[mingcute--ai-line]',
				tooltip: 'Use Existing Template',
				function: async () => {
					templateSelector.open = true;
					return '';
				}
			}}
		/>

		<CustomValueInput
			label="Cloud-Init Meta Data"
			placeholder={cloudInitPlaceholders.metadata}
			bind:value={cloudInit.metadata}
			classes="flex-1 space-y-1.5"
			type="textarea"
		/>

		<CustomValueInput
			label="Cloud-Init Network Config"
			placeholder={cloudInitPlaceholders.networkConfig}
			bind:value={cloudInit.networkConfig}
			classes="flex-1 space-y-1.5"
			type="textarea"
		/>
	{/if}
</div>

{#if templateSelector.open}
	<Dialog.Root bind:open={templateSelector.open}>
		<Dialog.Content class="overflow-hidden p-5 max-w-[320px]!">
			<Dialog.Header>
				<div class="flex items-center justify-between">
					<div class="flex items-center gap-2">
						<span class="icon-[mdi--cloud-upload-outline] h-5 w-5"></span>
						<span>Select a Template</span>
					</div>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							templateSelector.open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Header>

			<SimpleSelect
				options={cloudInitTemplates.current.map((template) => ({
					label: template.name,
					value: template.id.toString()
				}))}
				placeholder="Select a Template"
				bind:value={templateSelector.current}
				onChange={(e: string) => {
					const template = cloudInitTemplates.current.find((t) => t.id.toString() === e);
					cloudInit.data = template?.user || '';
					cloudInit.metadata = template?.meta || '';
					cloudInit.networkConfig = template?.networkConfig || '';
					templateSelector.open = false;
				}}
			/>
		</Dialog.Content>
	</Dialog.Root>
{/if}
