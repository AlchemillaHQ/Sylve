<script lang="ts">
	import { getRAMInfoResult } from '$lib/api/info/ram';
	import { modifyRAM } from '$lib/api/jail/hardware';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import type { RAMInfo } from '$lib/types/info/ram';
	import type { Jail } from '$lib/types/jail/jail';
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
		ram: RAMInfo;
		jail: Jail;
		node: string;
		onSaved: () => void | Promise<void>;
	}

	let { open = $bindable(), ram, jail, node, onSaved }: Props = $props();
	let ramValue = $derived(formatBytesBinary(jail.memory || 1));
	let saving = $state(false);

	const hostRam = resource(
		() => node,
		async (hostname) => {
			const result = await getRAMInfoResult({ hostname });

			if (isAPIResponse(result)) {
				handleAPIError(result);
				return ram;
			}

			await updateCache('ram-info', result, hostname);
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

		const parsed = parseSizeInputToBytes(ramValue);
		if (parsed === null) {
			error = 'Invalid RAM value';
		} else {
			bytes = parsed;
		}

		if (bytes < 1024 * 1024) {
			error = 'RAM value must be at least 1 MiB';
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

		saving = true;
		try {
			const response = await modifyRAM(jail.ctId, bytes, { hostname: node });
			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to modify RAM', {
					position: 'bottom-center'
				});
				return;
			}

			await onSaved();
			toast.success('RAM modified', {
				position: 'bottom-center'
			});
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/4 overflow-hidden p-6 lg:max-w-2xl"
		showResetButton={true}
		onReset={() => {
			if (!saving) ramValue = formatBytesBinary(jail.memory || 1);
		}}
		onClose={() => {
			if (saving) return;
			ramValue = formatBytesBinary(jail.memory || 1);
			open = false;
		}}
	>
		<Dialog.Header class="">
			<Dialog.Title>
				<SpanWithIcon icon="icon-[ri--ram-fill]" size="h-5 w-5" gap="gap-2" title="RAM" />
			</Dialog.Title>
		</Dialog.Header>

		<CustomValueInput
			placeholder="1.0 GiB"
			bind:value={ramValue}
			classes="flex-1 space-y-1"
			onBlur={() => {
				const normalized = normalizeSizeInputExact(ramValue);
				if (normalized !== null) {
					ramValue = normalized;
				}
			}}
		/>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button
					onclick={modify}
					type="submit"
					size="sm"
					disabled={saving || hostRam.loading}
					aria-busy={saving}
				>
					{#if saving}
						<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>
						Saving...
					{:else}
						Save
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
