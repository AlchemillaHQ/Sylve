<script lang="ts">
	import { modifyVNC } from '$lib/api/vm/hardware';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Input from '$lib/components/ui/input/input.svelte';
	import { Label } from '$lib/components/ui/label/index.js';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';
	import { generatePassword, parseBoolean } from '$lib/utils/string';
	import { resolutions } from '$lib/utils/vm/vnc';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		vm: VM | null;
		vms: VM[];
	}

	let { open = $bindable(), vm, vms }: Props = $props();

	let options = {
		port: vm?.vncPort || 5900,
		resolution: vm?.vncResolution || '640x480',
		password: vm?.vncPassword || 'sigma-chad-password-never',
		wait: vm?.vncWait ?? false,
		resolutionOpen: false,
		vncEnabledOpen: false,
		vncEnabled: (vm?.vncEnabled || false).toString()
	};

	let properties = $state(options);

	async function modify() {
		if (!vm) return;

		let error = '';

		if (!properties.password || properties.password.length < 8) {
			error = 'Password too short';
		}

		if (properties.port < 5900 || properties.port > 65535) {
			error = 'Port must be between 5900 and 65535';
		}

		if (!properties.resolution || !resolutions.some((r) => r.value === properties.resolution)) {
			error = 'Invalid resolution selected';
		}

		const otherVm = vms.find(
			(v) => v.id !== vm.id && Number(v.vncPort) === Number(properties.port)
		);

		if (otherVm) {
			error = 'VNC port already in use';
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
		}

		const response = await modifyVNC(
			vm.rid,
			parseBoolean(properties.vncEnabled),
			Number(properties.port),
			properties.resolution,
			properties.password,
			properties.wait ?? false
		);

		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to modify VNC', {
				position: 'bottom-center'
			});
		} else {
			toast.success('VNC modified', {
				position: 'bottom-center'
			});
			open = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-1/3 overflow-hidden p-5 lg:max-w-2xl">
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon-[arcticons--vncviewer] h-5 w-5"></span>

					<span>VNC</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4 "
						onclick={() => {
							properties = options;
						}}
					>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Reset'}</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							properties = options;
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<CustomComboBox
			bind:open={properties.vncEnabledOpen}
			label="Status"
			bind:value={properties.vncEnabled}
			data={[
				{ value: 'true', label: 'Enabled' },
				{ value: 'false', label: 'Disabled' }
			]}
		></CustomComboBox>

		<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
			<CustomComboBox
				bind:open={properties.resolutionOpen}
				label="VNC Resolution"
				bind:value={properties.resolution}
				data={resolutions}
				classes="flex-1 space-y-1.5"
				placeholder="Select VNC resolution"
				triggerWidth="w-full "
				width="w-full"
			></CustomComboBox>

			<CustomValueInput
				label="Port"
				type="number"
				bind:value={properties.port}
				placeholder="5900"
				classes="space-y-1"
			/>
		</div>

		<div class="grid grid-cols-1">
			<div class="space-y-1">
				<Label class="w-24 whitespace-nowrap text-sm">Password</Label>
				<div class="flex w-full items-center space-x-2">
					<Input
						type="password"
						id="vnc-password"
						placeholder="Enter or generate password"
						class="w-full"
						autocomplete="off"
						bind:value={properties.password}
						showPasswordOnFocus={true}
					/>

					<Button
						onclick={() => {
							properties.password = generatePassword();
						}}
					>
						<span class="icon-[fad--random-2dice] h-6 w-6"></span>
					</Button>
				</div>
			</div>
		</div>

		<div class="flex items-center space-x-2">
			<Checkbox id="wait" bind:checked={properties.wait} />
			<Label for="wait" class="text-sm font-medium">Wait for VNC</Label>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
