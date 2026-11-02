<script lang="ts">
	import { createContainerNetwork } from '$lib/api/container/resources';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { SwitchList } from '$lib/types/network/switch';
	import { toastContainerError } from '$lib/utils/container/errors';
	import { isAPIErrorResponse } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		node: string;
		switches: SwitchList;
		afterChange: () => void;
	}

	let { open = $bindable(), node, switches, afterChange }: Props = $props();

	let form = $state({
		name: '',
		switch: '',
		subnet: '',
		gateway: '',
		rangeStart: '',
		rangeEnd: ''
	});
	let saving = $state(false);

	let switchOptions = $derived([
		{ value: '', label: 'None (NAT bridge)' },
		...[...switches.standard, ...switches.manual].map((sw) => ({ value: sw.name, label: sw.name }))
	]);

	async function save() {
		saving = true;
		const result = await createContainerNetwork(form, node);
		saving = false;
		if (isAPIErrorResponse(result)) {
			toastContainerError(result);
			return;
		}

		toast.success(`Network ${result.name} created`, { position: 'bottom-center' });
		open = false;
		afterChange();
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-[96%] p-5 md:max-w-xl"
		showCloseButton={!saving}
		onClose={() => (open = false)}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon icon="icon-[mdi--lan]" size="h-5 w-5" gap="gap-2" title="New Network" />
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
			<CustomValueInput label="Name" placeholder="web" bind:value={form.name} />
			<SimpleSelect
				label="Switch"
				placeholder="None (NAT bridge)"
				options={switchOptions}
				bind:value={form.switch}
				onChange={(value) => (form.switch = value)}
				classes={{
					parent: 'space-y-1.5',
					label: 'flex h-7 items-center text-sm',
					trigger: 'w-full'
				}}
			/>
			<CustomValueInput
				label="Subnet"
				placeholder={form.switch ? '192.168.1.0/24' : 'Auto'}
				bind:value={form.subnet}
			/>
			<CustomValueInput
				label="Gateway"
				placeholder={form.switch ? '192.168.1.1' : 'Auto'}
				bind:value={form.gateway}
			/>
			<CustomValueInput label="Range Start" placeholder="Optional" bind:value={form.rangeStart} />
			<CustomValueInput label="Range End" placeholder="Optional" bind:value={form.rangeEnd} />
		</div>

		<Dialog.Footer>
			<Button size="sm" onclick={save} disabled={saving}>
				{#if saving}
					<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
				{/if}
				Create
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
