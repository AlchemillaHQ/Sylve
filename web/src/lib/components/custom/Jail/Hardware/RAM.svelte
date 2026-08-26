<script lang="ts">
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
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

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

		if (bytes > ram.total - 1024 * 1024 * 1024 || bytes > ram.total) {
			if (bytes > ram.total) {
				error = 'RAM value exceeds available memory';
			} else if (bytes > ram.total - 1024 * 1024 * 1024) {
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
				<Button onclick={modify} type="submit" size="sm" disabled={saving} aria-busy={saving}>
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
