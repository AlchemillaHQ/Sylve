<script lang="ts">
	import { getRAMInfoResult } from '$lib/api/info/ram';
	import { modifyRAM } from '$lib/api/vm/hardware';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { RAMInfo } from '$lib/types/info/ram';
	import type { VM } from '$lib/types/vm/vm';
	import {
		formatBytesBinary,
		normalizeSizeInputExact,
		parseSizeInputToBytes
	} from '$lib/utils/bytes';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';
	import { untrack } from 'svelte';

	interface Props {
		open: boolean;
		node: string;
		ram: RAMInfo;
		vm: VM | null;
		reload: boolean;
	}

	let { open = $bindable(), node, ram, vm, reload = $bindable(false) }: Props = $props();

	// svelte-ignore state_referenced_locally
	let options = {
		ram: formatBytesBinary(vm?.ram || 1)
	};

	let properties = $state(options);
	let saving = $state(false);

	const hostRam = resource(
		() => node,
		async (hostname) => {
			const result = await getRAMInfoResult({ hostname });

			if (isAPIResponse(result)) {
				handleAPIError(result);
				return ram;
			}

			await updateCache('system-ram-info', result, hostname);
			return result;
		},
		{
			initialValue: untrack(() => ram)
		}
	);

	async function modify() {
		if (saving) return;

		let bytes: number = 0;
		let error: string = '';

		const parsed = parseSizeInputToBytes(properties.ram);
		if (parsed === null) {
			error = 'Invalid RAM value';
		} else {
			bytes = parsed;
		}

		if (bytes < 128 * 1024 * 1024) {
			error = 'RAM value must be at least 128 MiB';
		}

		const total = hostRam.current.total;

		if (bytes > total - 1024 * 1024 * 1024 || bytes > total) {
			if (bytes > total) {
				error = 'RAM value exceeds available memory';
			} else if (bytes > total - 1024 * 1024 * 1024) {
				error = 'RAM value is too high, at least 1 GiB must be reserved for the host';
			}
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
		}

		if (vm) {
			saving = true;
			try {
				const response = await modifyRAM(vm.rid, bytes, { hostname: node });

				if (response.status !== 'success') {
					handleAPIError(response);
					toast.error('Failed to modify RAM', {
						position: 'bottom-center'
					});
					return;
				}

				reload = true;
				toast.success(
					response.message === 'no_changes_detected' ? 'No RAM changes needed' : 'RAM modified',
					{ position: 'bottom-center' }
				);
				open = false;
			} finally {
				saving = false;
			}
		} else {
			toast.error('VM not found', {
				position: 'bottom-center'
			});
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/4 overflow-hidden p-5 lg:max-w-2xl"
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
				<SpanWithIcon icon="icon-[ri--ram-fill]" size="h-5 w-5" gap="gap-2" title="RAM" />
			</Dialog.Title>
		</Dialog.Header>

		<CustomValueInput
			label=""
			placeholder="1.0 GiB"
			bind:value={properties.ram}
			classes="flex-1 space-y-1"
			onBlur={() => {
				const normalized = normalizeSizeInputExact(properties.ram);
				if (normalized !== null) {
					properties.ram = normalized;
				}
			}}
		/>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm" disabled={saving || hostRam.loading}>
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
