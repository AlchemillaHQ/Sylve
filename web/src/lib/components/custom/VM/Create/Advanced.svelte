<script lang="ts">
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import {
		default as ComboBox,
		default as CustomComboBox
	} from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
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
		vncEnabled: boolean;
		serial: boolean;
		vncPort: number;
		vncBind: string;
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
		extraBhyveOptionsEnabled: boolean;
		extraBhyveOptions: string;
		ignoreUmsrs: boolean;
		qemuGuestAgent: boolean;
	}

	let {
		vncEnabled = $bindable(),
		serial = $bindable(),
		vncPort = $bindable(),
		vncBind = $bindable(),
		vncPassword = $bindable(),
		vncWait = $bindable(),
		vncResolution = $bindable(),
		startAtBoot = $bindable(),
		bootOrder = $bindable(),
		tpmEmulation = $bindable(),
		timeOffset = $bindable(),
		cloudInit = $bindable(),
		extraBhyveOptionsEnabled = $bindable(),
		extraBhyveOptions = $bindable(),
		ignoreUmsrs = $bindable(),
		qemuGuestAgent = $bindable()
	}: Props = $props();

	onMount(() => {
		if (!vncBind) vncBind = '127.0.0.1';
		if (vncEnabled && !vncPort) vncPort = Math.floor(Math.random() * (5999 - 5900 + 1)) + 5900;
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

	watch(
		() => vncEnabled,
		(enabled) => {
			if (!enabled) {
				vncPort = 0;
				return;
			}

			if (!vncPort) {
				vncPort = Math.floor(Math.random() * (5999 - 5900 + 1)) + 5900;
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

	watch(
		() => vncEnabled,
		(enabled) => {
			if (!enabled) {
				vncWait = false;
			}
		}
	);

	watch(
		() => extraBhyveOptionsEnabled,
		(enabled) => {
			if (!enabled) {
				extraBhyveOptions = '';
			}
		}
	);
</script>

<div class="flex flex-col gap-4 space-y-1.5 p-4">
	<div class="grid grid-cols-1 gap-4 lg:grid-cols-8">
		<CustomComboBox
			bind:open={resolutionOpen}
			label="VNC Resolution"
			bind:value={vncResolution}
			data={resolutions}
			classes="flex-1 space-y-1.5 lg:col-span-2"
			placeholder="Select VNC resolution"
			triggerWidth="w-full"
			width="w-full"
			disabled={!vncEnabled}
		></CustomComboBox>

		<CustomValueInput
			label="VNC Password"
			placeholder="Enter or generate passphrase"
			type="password"
			bind:value={vncPassword}
			classes="flex-1 space-y-1.5 lg:col-span-3"
			disabled={!vncEnabled}
			revealOnFocus={true}
			topRightButton={{
				icon: 'icon-[fad--random-2dice]',
				tooltip: 'Generate Password',
				function: async () => generatePassword()
			}}
		/>

		<CustomValueInput
			label="VNC Bind IP"
			placeholder="127.0.0.1"
			bind:value={vncBind}
			classes="flex-1 space-y-1.5 lg:col-span-2"
			disabled={!vncEnabled}
		/>
		<CustomValueInput
			label="VNC Port"
			placeholder="5900"
			bind:value={vncPort}
			classes="flex-1 space-y-1.5 lg:col-span-1"
			disabled={!vncEnabled}
		/>
	</div>

	<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
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

	<div class="mt-1 grid grid-cols-2 gap-4 lg:grid-cols-4">
		<CustomCheckbox label="Enable VNC" bind:checked={vncEnabled} classes="flex items-center gap-2"
		></CustomCheckbox>

		<CustomCheckbox
			label="VNC Wait"
			bind:checked={vncWait}
			classes="flex items-center gap-2"
			disabled={!vncEnabled}
		></CustomCheckbox>

		<CustomCheckbox label="Serial Console" bind:checked={serial} classes="flex items-center gap-2"
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
			label="Extra Bhyve Options"
			bind:checked={extraBhyveOptionsEnabled}
			classes="flex items-center gap-2"
		></CustomCheckbox>

		<CustomCheckbox
			label="Ignore UMSRs"
			bind:checked={ignoreUmsrs}
			classes="flex items-center gap-2"
		></CustomCheckbox>

		<CustomCheckbox label="QEMU GA" bind:checked={qemuGuestAgent} classes="flex items-center gap-2"
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

	{#if extraBhyveOptionsEnabled}
		<CustomValueInput
			label="Extra Bhyve Options"
			placeholder="-S\n-u"
			bind:value={extraBhyveOptions}
			classes="flex-1 space-y-1.5"
			type="textarea"
			textAreaClasses="h-32 font-mono text-xs"
			hint="One option per line. These raw args are prepended before Sylve-generated bhyve args."
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
						title="Close"
						onclick={() => {
							templateSelector.open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
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
