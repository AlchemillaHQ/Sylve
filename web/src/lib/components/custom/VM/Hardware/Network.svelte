<script lang="ts">
	import { attachNetwork } from '$lib/api/vm/network';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { SwitchList } from '$lib/types/network/switch';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';
	import { generateMACOptions } from '$lib/utils/network/object';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		switches: SwitchList;
		vm: VM | null;
		networkObjects: NetworkObject[];
	}

	let { open = $bindable(), switches, vm, networkObjects }: Props = $props();
	let usable = $derived.by(() => {
		return [
			...(switches.standard ?? []).map((s) => ({
				...s,
				uid: `standard-${s.id}`
			})),
			...(switches.manual ?? []).map((s) => ({
				...s,
				uid: `manual-${s.id}`
			}))
		];
	});

	let usableMacs = $derived.by(() => {
		return networkObjects.filter(
			(obj) => obj.type === 'Mac' && obj.entries?.length === 1 && obj.isUsed === false
		);
	});

	let options = {
		emulation: '',
		mac: {
			open: false,
			value: '0'
		},
		switchId: ''
	};

	let properties = $state(options);

	async function addNetwork() {
		let error = '';

		if (!properties.switchId) {
			error = 'Switch is required';
		} else if (!properties.emulation) {
			error = 'Emulation is required';
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
		}

		const response = await attachNetwork(
			vm?.rid ?? 0,
			properties.switchId,
			properties.emulation,
			properties.mac.value !== '0' ? Number(properties.mac.value) : 0
		);

		if (response.error) {
			handleAPIError(response);
			toast.error('Error attaching VM to switch', {
				position: 'bottom-center'
			});
			return;
		} else {
			toast.success('VM attached to switch', {
				position: 'bottom-center'
			});
			open = false;
			properties = options;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-md overflow-hidden p-5 lg:max-w-2xl">
		<Dialog.Header class="">
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--network] h-5 w-5"></span>

					<span>New Network</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4"
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

		<SimpleSelect
			label="Switch"
			placeholder="Select Switch"
			options={usable?.map((s) => ({
				value: s.name,
				label: s.name
			})) || []}
			bind:value={properties.switchId}
			onChange={(value) => (properties.switchId = value)}
		/>

		<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
			<SimpleSelect
				label="Emulation"
				placeholder="Select Emulation"
				options={[
					{ value: 'virtio', label: 'VirtIO' },
					{ value: 'e1000', label: 'E1000' }
				]}
				bind:value={properties.emulation}
				onChange={(value) => (properties.emulation = value)}
			/>

			<CustomComboBox
				bind:open={properties.mac.open}
				label={'MAC'}
				bind:value={properties.mac.value}
				data={generateMACOptions(usableMacs)}
				classes="flex-1 space-y-1"
				placeholder="Select MAC"
				width="w-3/4"
				multiple={false}
			></CustomComboBox>
		</div>
		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={addNetwork} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
