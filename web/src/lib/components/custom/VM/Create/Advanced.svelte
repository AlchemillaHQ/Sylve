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

	import Icon from '@iconify/svelte';
	import { onMount } from 'svelte';

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
		timeOffset = $bindable()
	}: Props = $props();

	onMount(() => {
		vncPort = Math.floor(Math.random() * (5999 - 5900 + 1)) + 5900;
	});

	let timeOffsetOpen = $state(false);
	const timeOffsets = [
		{ label: 'UTC', value: 'utc' },
		{ label: 'Local Time', value: 'localtime' }
	];

	let resolutionOpen = $state(false);
	const resolutions = [
		{ label: '1024x768', value: '1024x768' },
		{ label: '1280x720', value: '1280x720' },
		{ label: '1920x1080', value: '1920x1080' },
		{ label: '2560x1440', value: '2560x1440' },
		{ label: '3840x2160', value: '3840x2160' }
	];
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
					<Icon
						icon="fad:random-2dice"
						class="h-6 w-6"
						onclick={() => {
							vncPassword = generatePassword();
						}}
					/>
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

	<div class="mt-1 grid grid-cols-1 gap-4 lg:grid-cols-2">
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
			label="TPM Emulation (Experimental)"
			bind:checked={tpmEmulation}
			classes="flex items-center gap-2"
		></CustomCheckbox>
	</div>
</div>
