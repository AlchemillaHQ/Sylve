<script lang="ts">
	import {
		exportDirectoryRecovery,
		getDirectoryRecoveryInfo,
		restoreDirectoryRecovery,
		rollbackDirectoryRecovery
	} from '$lib/api/directory-services';
	import FilePond from '$lib/components/custom/FilePond.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import type { ActiveDirectory } from '$lib/types/directory-services';
	import { APIRequestError, getAPIErrorText } from '$lib/utils/http';
	import { formatDirectoryServiceError } from '$lib/utils/directory-services';
	import { FileStatus, type FilePond as FilePondInstance, type FilePondFile } from 'filepond';
	import { onDestroy, onMount, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	let {
		open = $bindable(),
		node,
		ad,
		reload = $bindable(false)
	}: {
		open: boolean;
		node: string;
		ad: ActiveDirectory;
		reload?: boolean;
	} = $props();
	const targetNode = untrack(() => node);
	let mounted = true;
	let tab = $state('export');
	let pending = $state(false);
	let loading = $state(true);
	let infoLoaded = $state(false);
	let busy = $state(false);
	let passphrase = $state('');
	let repeatPassphrase = $state('');
	let computerName = $state('');
	let manageServices = $state(false);
	let pond = $state<FilePondInstance>();
	let error = $state('');
	onDestroy(() => {
		mounted = false;
		passphrase = '';
		repeatPassphrase = '';
	});
	onMount(async () => {
		try {
			const info = await getDirectoryRecoveryInfo({ hostname: targetNode });
			if (!mounted) return;
			pending = info.pending;
			infoLoaded = true;
			if (pending) tab = 'restore';
		} catch {
			if (mounted)
				error = 'Recovery information could not be loaded. Local administrator access is required.';
		} finally {
			if (mounted) loading = false;
		}
	});
	function acceptRecoveryFile(item: FilePondFile) {
		if (busy || loading) return false;
		if (item.fileSize > 64 * 1024 * 1024) {
			error = 'Select an encrypted recovery file of no more than 64 MiB.';
			return false;
		}
		return true;
	}
	async function submit() {
		if (busy || loading || !infoLoaded) return;
		error = '';
		if (!manageServices) {
			error = 'Confirm the service interruption before continuing.';
			return;
		}
		const passphraseBytes = new TextEncoder().encode(passphrase).length;
		if (passphraseBytes < 12 || passphraseBytes > 1024) {
			error = 'Use a recovery passphrase of 12 to 1024 bytes.';
			return;
		}
		if (computerName !== ad.computerName) {
			error = `Enter ${ad.computerName} to confirm this node.`;
			return;
		}
		if (tab === 'export' && passphrase !== repeatPassphrase) {
			error = 'The recovery passphrases do not match.';
			return;
		}
		const selectedFile = pond?.getFile();
		const file = selectedFile?.status === FileStatus.IDLE ? selectedFile.file : null;
		if (tab === 'restore' && !pending && (!file || file.size > 64 * 1024 * 1024)) {
			error = 'Select an encrypted recovery file of no more than 64 MiB.';
			return;
		}
		busy = true;
		try {
			if (tab === 'export') {
				const result = await exportDirectoryRecovery(passphrase, computerName, manageServices, {
					hostname: targetNode
				});
				if (!mounted) return;
				const bytes = Uint8Array.from(atob(result.file), (value) => value.charCodeAt(0));
				const url = URL.createObjectURL(new Blob([bytes], { type: 'application/octet-stream' }));
				const anchor = document.createElement('a');
				anchor.href = url;
				anchor.download = `sylve-ad-${ad.computerName.toLowerCase()}-${new Date().toISOString().slice(0, 10)}.sylve-ad`;
				anchor.click();
				URL.revokeObjectURL(url);
				toast.success(
					'Encrypted recovery bundle downloaded. Keep it and its passphrase in separate secure locations.',
					{ position: 'bottom-center' }
				);
				reload = true;
				if (result.warning) {
					error = formatDirectoryServiceError(result.warning);
					passphrase = '';
					repeatPassphrase = '';
					return;
				}
			} else {
				let encoded = '';
				if (!pending && file) {
					const bytes = new Uint8Array(await file.arrayBuffer());
					if (!mounted) return;
					const chunks: string[] = [];
					for (let start = 0; start < bytes.length; start += 32768)
						chunks.push(String.fromCharCode(...bytes.subarray(start, start + 32768)));
					encoded = btoa(chunks.join(''));
				}
				const result = pending
					? await rollbackDirectoryRecovery(passphrase, computerName, manageServices, {
							hostname: targetNode
						})
					: await restoreDirectoryRecovery(passphrase, computerName, encoded, manageServices, {
							hostname: targetNode
						});
				if (!mounted) return;
				if (result.status === 'error') {
					reload = true;
					error = formatDirectoryServiceError(getAPIErrorText(result));
					try {
						const info = await getDirectoryRecoveryInfo({ hostname: targetNode });
						if (mounted) pending = info.pending;
					} catch {
						infoLoaded = false;
					}
					return;
				}
				reload = true;
				toast.success(
					'Recovery state applied. Services paused by recovery were resumed; services that were already stopped before recovery remain stopped. Check the connection.',
					{ position: 'bottom-center' }
				);
			}
			passphrase = '';
			repeatPassphrase = '';
			open = false;
		} catch (cause) {
			if (!mounted) return;
			reload = true;
			error =
				cause instanceof APIRequestError
					? formatDirectoryServiceError(getAPIErrorText(cause.response))
					: 'The recovery operation could not be completed. Check the node connection and try again.';
		} finally {
			busy = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="max-h-[90dvh] overflow-y-auto sm:max-w-2xl"
		aria-describedby={undefined}
		showCloseButton={!busy}
		onEscapeKeydown={(event) => {
			if (busy) event.preventDefault();
		}}
		onClose={() => {
			if (!busy) open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title
				><SpanWithIcon
					icon="icon-[mdi--backup-restore]"
					size="h-5 w-5"
					gap="gap-2"
					title="Directory Services Recovery"
				/></Dialog.Title
			>
		</Dialog.Header>
		<Tabs.Root bind:value={tab} class="gap-4">
			<Tabs.List class="grid w-full grid-cols-2 p-0">
				<Tabs.Trigger value="export" class="border-b" disabled={busy || pending}
					>Backup</Tabs.Trigger
				>
				<Tabs.Trigger value="restore" class="border-b" disabled={busy}
					>{pending ? 'Rollback' : 'Restore'}</Tabs.Trigger
				>
			</Tabs.List>
			<Card.Root class="border-yellow-500/30 bg-yellow-500/10 p-0!" role="note">
				<Card.Content
					class="flex items-start gap-2 p-3 text-sm text-yellow-600 dark:text-yellow-400"
				>
					<span class="icon-[material-symbols--warning] mt-0.5 size-4 shrink-0" aria-hidden="true"
					></span>
					<div class="space-y-2">
						<p>
							Sylve temporarily stops running Samba and Winbind services to keep recovery state
							consistent. SMB clients will be disconnected. After completion or a safe rollback,
							only previously running services are resumed. Incomplete recovery leaves them stopped.
						</p>
						{#if pending}<p>
								An interrupted restore is pending. Enter the passphrase used for that restore to
								return to the protected previous state.
							</p>
						{:else if tab === 'export'}<p>
								The encrypted bundle includes machine-trust secrets, ID-allocation databases,
								Kerberos credentials, the encryption key and a Sylve database snapshot. It does not
								back up share data or the domain controller.
							</p>
						{:else}<p>
								Restore requires the same node and saved configuration, plus a reachable domain
								controller to verify the saved trust. It does not rejoin the domain, replace
								unrelated Sylve settings, edit shares or change filesystem ACLs. Older credentials
								or changed share identities are rejected. Existing ID allocations and the Samba
								version must match the snapshot. Transient Samba caches are cleared.
							</p>{/if}
					</div>
				</Card.Content>
			</Card.Root>
			<div class="grid gap-4 sm:grid-cols-2">
				<CustomValueInput
					label="Confirm Computer Name"
					placeholder={ad.computerName}
					bind:value={computerName}
					classes={tab === 'export' ? 'space-y-1.5 sm:col-span-2' : 'space-y-1.5'}
					disabled={busy || loading}
				/>
				<CustomValueInput
					label="Recovery Passphrase"
					placeholder="Recovery passphrase"
					type="password"
					bind:value={passphrase}
					autocomplete="new-password"
					disabled={busy || loading}
					hint="At least 12 bytes. This passphrase is not saved."
				/>
				{#if tab === 'export'}<CustomValueInput
						label="Repeat Passphrase"
						placeholder="Repeat recovery passphrase"
						type="password"
						bind:value={repeatPassphrase}
						autocomplete="new-password"
						disabled={busy || loading}
					/>{/if}
			</div>
			{#if tab === 'restore' && !pending}
				<div class="space-y-2">
					<label for="directory-recovery-file" class="text-sm font-medium"
						>Encrypted Recovery File</label
					>
					<FilePond
						id="directory-recovery-file"
						bind:instance={pond}
						class="min-h-18! max-h-none! mb-1!"
						acceptedFileTypes={['.sylve-ad']}
						server={null}
						instantUpload={false}
						allowProcess={false}
						allowMultiple={false}
						maxFiles={1}
						allowPaste={false}
						allowImagePreview={false}
						allowImageExifOrientation={false}
						allowRemove={!busy && !loading}
						disabled={busy || loading}
						beforeDropFile={(item: FilePondFile | string) => typeof item !== 'string'}
						beforeAddFile={acceptRecoveryFile}
						labelIdle="Drag & drop an encrypted recovery file or <span class='filepond--label-action'>Browse</span>"
						credits={false}
					/>
				</div>
			{/if}
			<CustomCheckbox
				label="Allow Sylve to temporarily stop Samba and Winbind"
				bind:checked={manageServices}
				disabled={busy || loading}
			/>
			{#if error}<p
					class="text-destructive rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm"
					role="alert"
				>
					{error}
				</p>{/if}
		</Tabs.Root>
		<Dialog.Footer>
			<Button
				onclick={submit}
				disabled={busy || loading || !infoLoaded || !manageServices}
				variant={tab === 'restore' ? 'destructive' : 'default'}
			>
				{#if busy}<span class="icon-[mdi--loading] size-4 animate-spin"
					></span>Working...{:else}{tab === 'export'
						? 'Download Backup'
						: pending
							? 'Roll Back Restore'
							: 'Restore State'}{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
