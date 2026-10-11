<script lang="ts">
	import { saveKerberosKeytab } from '$lib/api/directory-services';
	import FilePond from '$lib/components/custom/FilePond.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { KerberosKeytab } from '$lib/types/directory-services';
	import { handleAPIError } from '$lib/utils/http';
	import { FileStatus, type FilePond as FilePondInstance, type FilePondFile } from 'filepond';
	import { toast } from 'svelte-sonner';
	import { onDestroy, untrack } from 'svelte';

	let {
		open = $bindable(),
		node,
		keytab = null,
		reload = $bindable(false)
	}: { open: boolean; node: string; keytab?: KerberosKeytab | null; reload?: boolean } = $props();
	const targetNode = untrack(() => node);
	let mounted = true;
	onDestroy(() => {
		mounted = false;
	});
	let name = $state(untrack(() => keytab?.name ?? ''));
	let pond = $state<FilePondInstance>();
	let saving = $state(false);
	function reset() {
		name = keytab?.name ?? '';
		pond?.removeFiles();
	}
	function acceptKeytabFile(item: FilePondFile) {
		if (saving) return false;
		if (item.fileSize > 1024 * 1024) {
			toast.error('Keytabs must be at most 1 MiB', { position: 'bottom-center' });
			return false;
		}
		return true;
	}
	async function save() {
		if (saving) return;
		const selectedFile = pond?.getFile();
		if (selectedFile && selectedFile.status !== FileStatus.IDLE) {
			toast.error('Wait for a valid keytab file before saving', { position: 'bottom-center' });
			return;
		}
		const file = selectedFile?.file ?? null;
		if (!name.trim() || (!keytab && !file)) {
			toast.error('A name and keytab file are required', { position: 'bottom-center' });
			return;
		}
		if (file && file.size > 1024 * 1024) {
			toast.error('Keytabs must be at most 1 MiB', { position: 'bottom-center' });
			return;
		}
		saving = true;
		try {
			let encoded = '';
			if (file) {
				const bytes = new Uint8Array(await file.arrayBuffer());
				if (!mounted) return;
				let binary = '';
				for (const byte of bytes) binary += String.fromCharCode(byte);
				encoded = btoa(binary);
			}
			const result = await saveKerberosKeytab(keytab?.id ?? null, name.trim(), encoded, {
				hostname: targetNode
			});
			if (!mounted) return;
			if (result.status === 'error') {
				handleAPIError(result);
				return;
			}
			toast.success(`Kerberos keytab ${keytab ? 'updated' : 'uploaded'}`, {
				position: 'bottom-center'
			});
			reload = true;
			reset();
			open = false;
		} catch {
			if (!mounted) return;
			toast.error('Unable to read the keytab file', { position: 'bottom-center' });
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="max-h-[90vh] overflow-y-auto sm:max-w-2xl"
		showCloseButton={!saving}
		showResetButton={keytab !== null && !saving}
		onEscapeKeydown={(event) => {
			if (saving) event.preventDefault();
		}}
		onReset={reset}
		onClose={() => {
			reset();
			open = false;
		}}
	>
		<Dialog.Header class="-mb-2">
			<Dialog.Title
				><SpanWithIcon
					icon="icon-[mdi--key-chain]"
					size="h-5 w-5"
					gap="gap-2"
					title={keytab ? `Edit Kerberos Keytab - ${keytab.name}` : 'Upload Kerberos Keytab'}
				/></Dialog.Title
			>
			<Dialog.Description class="text-muted-foreground text-xs"
				>Keytabs contain credentials. Sylve validates and securely stores the file; key material is
				not shown.</Dialog.Description
			>
		</Dialog.Header>
		<CustomValueInput
			label="Name"
			placeholder="Storage service"
			bind:value={name}
			classes="space-y-1.5"
			disabled={saving}
		/>
		<FilePond
			bind:instance={pond}
			class="min-h-18! max-h-none! mb-1!"
			server={null}
			instantUpload={false}
			allowProcess={false}
			allowMultiple={false}
			maxFiles={1}
			allowPaste={false}
			allowImagePreview={false}
			allowImageExifOrientation={false}
			allowRemove={!saving}
			disabled={saving}
			beforeDropFile={(item: FilePondFile | string) => typeof item !== 'string'}
			beforeAddFile={acceptKeytabFile}
			labelIdle={keytab
				? 'Drag & drop a replacement keytab (optional) or <span class="filepond--label-action">Browse</span>'
				: 'Drag & drop a Kerberos keytab file or <span class="filepond--label-action">Browse</span>'}
			credits={false}
		/>
		{#if keytab}<p class="text-muted-foreground break-all text-xs">
				Current principals: {keytab.principals.join(', ')}
			</p>{/if}
		<Dialog.Footer class="flex justify-end"
			><Button size="sm" disabled={saving} onclick={save}
				>{#if saving}<span class="icon-[mdi--loading] h-4 w-4 animate-spin"
					></span>Saving...{:else}{keytab ? 'Save Changes' : 'Upload Keytab'}{/if}</Button
			></Dialog.Footer
		>
	</Dialog.Content>
</Dialog.Root>
