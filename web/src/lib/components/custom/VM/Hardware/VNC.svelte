<script lang="ts">
	import { modifyVNC } from '$lib/api/vm/hardware';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';
	import { generatePassword, isValidIPv4, isValidIPv6 } from '$lib/utils/string';
	import { resolutions } from '$lib/utils/vm/vnc';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		node: string;
		vm: VM | null;
		vms: VM[];
		reload: boolean;
	}

	let { open = $bindable(), node, vm, vms, reload = $bindable(false) }: Props = $props();

	// svelte-ignore state_referenced_locally
	let options = {
		port: vm?.vncPort || 5900,
		bind: vm?.vncBind || '127.0.0.1',
		resolution: vm?.vncResolution || '640x480',
		password: vm?.vncPassword ?? '',
		wait: vm?.vncWait ?? false,
		resolutionOpen: false,
		vncEnabled: vm?.vncEnabled ?? false
	};

	let properties = $state(options);
	let saving = $state(false);

	async function modify() {
		if (!vm || saving) return;

		let error = '';
		const isVNCEnabled = properties.vncEnabled;

		if (!isValidIPv4(properties.bind) && !isValidIPv6(properties.bind)) {
			error = 'Bind IP must be a valid IPv4 or IPv6 address';
		}

		if (properties.password.includes(',')) {
			error = 'VNC password cannot contain commas';
		}

		if (isVNCEnabled) {
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
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
		}

		saving = true;
		try {
			const response = await modifyVNC(
				vm.rid,
				isVNCEnabled,
				Number(properties.port),
				properties.bind,
				properties.resolution,
				properties.password,
				properties.wait ?? false,
				{ hostname: node }
			);

			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error('Failed to modify VNC', {
					position: 'bottom-center'
				});
				return;
			}

			reload = true;
			toast.success(
				response.message === 'no_changes_detected' ? 'No VNC changes needed' : 'VNC modified',
				{ position: 'bottom-center' }
			);
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="overflow-hidden p-5 sm:max-w-xl"
		showResetButton={true}
		onReset={() => {
			properties = options;
		}}
		onClose={() => {
			properties = options;
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon icon="icon-[arcticons--vncviewer]" size="h-5 w-5" gap="gap-2" title="VNC" />
			</Dialog.Title>
		</Dialog.Header>

		<div class="flex flex-col gap-4">
			<div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
				<CustomValueInput
					label="Bind IP"
					bind:value={properties.bind}
					placeholder="127.0.0.1"
					classes="flex-1 space-y-1.5"
					disabled={!properties.vncEnabled || saving}
				/>

				<CustomValueInput
					label="Bind Port"
					type="number"
					bind:value={properties.port}
					placeholder="5900"
					classes="flex-1 space-y-1.5"
					disabled={!properties.vncEnabled || saving}
				/>

				<CustomComboBox
					bind:open={properties.resolutionOpen}
					label="Resolution"
					bind:value={properties.resolution}
					data={resolutions}
					classes="flex-1 space-y-1.5"
					placeholder="Select resolution"
					triggerWidth="w-full"
					width="w-full"
					disabled={!properties.vncEnabled || saving}
				></CustomComboBox>
			</div>

			<CustomValueInput
				label="Password"
				type="password"
				bind:value={properties.password}
				placeholder="Enter or generate password"
				classes="flex-1 space-y-1.5"
				revealOnFocus={true}
				disabled={!properties.vncEnabled || saving}
				topRightButton={{
					icon: 'icon-[fad--random-2dice]',
					tooltip: 'Generate Password',
					function: async () => generatePassword()
				}}
			/>

			<div class="flex items-center gap-4">
				<CustomCheckbox
					label="Enable VNC"
					bind:checked={properties.vncEnabled}
					classes="flex items-center gap-2"
					disabled={saving}
				/>
				<CustomCheckbox
					label="Wait for VNC"
					bind:checked={properties.wait}
					classes="flex items-center gap-2"
					disabled={!properties.vncEnabled || saving}
				/>
			</div>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm" disabled={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						Saving...
					{:else}
						Save
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
