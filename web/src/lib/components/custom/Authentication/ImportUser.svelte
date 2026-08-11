<script lang="ts">
	import { importUser, listImportableUsers } from '$lib/api/auth/local';
	import Button from '$lib/components/ui/button/button.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import type { ImportableUnixUser } from '$lib/types/auth';
	import { handleAPIError, isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { watch } from 'runed';
	import { onDestroy } from 'svelte';

	interface Props {
		open: boolean;
		reload?: boolean;
		hostname: string;
	}

	let { open = $bindable(), reload = $bindable(), hostname }: Props = $props();

	let importableUsers: ImportableUnixUser[] = $state([]);
	let loadingUsers = $state(false);
	let selectedUsername = $state({ open: false, value: '' });
	let password = $state('');
	let admin = $state(false);
	let submitting = $state(false);
	let listController: AbortController | null = null;

	let userOptions = $derived(
		importableUsers.map((user) => ({
			value: user.username,
			label: `${user.username} (UID: ${user.uid}, ${user.homeDirectory})`
		}))
	);

	async function loadImportableUsers() {
		listController?.abort();
		const controller = new AbortController();
		listController = controller;
		loadingUsers = true;
		try {
			const response = await listImportableUsers({ hostname, signal: controller.signal });
			if (controller.signal.aborted) return;
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Failed to load importable users', { position: 'bottom-center' });
				importableUsers = [];
				return;
			}
			importableUsers = response;
		} catch (error) {
			if (isRequestCancellation(error)) return;
			toast.error('Failed to load importable users', { position: 'bottom-center' });
			importableUsers = [];
		} finally {
			if (listController === controller) {
				listController = null;
				loadingUsers = false;
			}
		}
	}

	function reset() {
		selectedUsername = { open: false, value: '' };
		password = '';
		admin = false;
	}

	function closeDialog() {
		listController?.abort();
		reset();
		open = false;
	}

	onDestroy(() => listController?.abort());

	watch(
		() => `${open}:${hostname}`,
		() => {
			listController?.abort();
			if (open) {
				reset();
				loadImportableUsers();
			}
		}
	);

	function validate(): string {
		if (!selectedUsername.value) return 'Please select a user';
		if (password && password.length < 8) return 'Password must be at least 8 characters';
		return '';
	}

	async function submit() {
		if (submitting) return;
		const error = validate();
		if (error) {
			toast.error(error, { position: 'bottom-center' });
			return;
		}

		const username = selectedUsername.value;
		submitting = true;
		try {
			const response = await importUser(
				{
					username,
					password: password || undefined,
					admin
				},
				{ hostname }
			);
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Failed to import user', { position: 'bottom-center' });
				return;
			}

			reload = true;
			toast.success(`User "${response.username}" imported`, { position: 'bottom-center' });
			closeDialog();
		} catch {
			toast.error('Failed to import user', { position: 'bottom-center' });
		} finally {
			submitting = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		onInteractOutside={closeDialog}
		class="lg:max-w-lg w-[92%] gap-4 p-5"
		showCloseButton={true}
		onClose={closeDialog}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--import]"
					size="h-5 w-5"
					gap="gap-2"
					title="Import Unix User"
				/>
			</Dialog.Title>
		</Dialog.Header>

		{#if loadingUsers}
			<div class="text-muted-foreground flex h-32 items-center justify-center text-sm">
				Loading available users…
			</div>
		{:else if importableUsers.length === 0}
			<div
				class="text-muted-foreground flex h-32 flex-col items-center justify-center gap-2 text-sm"
			>
				<span class="icon-[mdi--account-off] h-8 w-8"></span>
				<p>No importable Unix users found</p>
				<p class="text-xs">
					All Unix users are either already registered in Sylve or reserved by the system.
				</p>
			</div>
		{:else}
			<div class="space-y-4">
				<CustomComboBox
					label=""
					placeholder="Select a Unix user to import…"
					bind:open={selectedUsername.open}
					bind:value={selectedUsername.value}
					data={userOptions}
					width="w-full"
				/>

				{#if selectedUsername.value}
					{@const info = importableUsers.find((user) => user.username === selectedUsername.value)}
					{#if info}
						<div class="bg-muted rounded-md p-3 text-sm space-y-1">
							<div class="flex justify-between">
								<span class="text-muted-foreground">UID / GID:</span>
								<span>{info.uid} / {info.gid}</span>
							</div>
							<div class="flex justify-between">
								<span class="text-muted-foreground">Shell:</span>
								<span>{info.shell || '-'}</span>
							</div>
							<div class="flex justify-between">
								<span class="text-muted-foreground">Home:</span>
								<span class="truncate">{info.homeDirectory || '-'}</span>
							</div>
						</div>
					{/if}

					<input type="text" style="display:none" autocomplete="username" />
					<input type="password" style="display:none" autocomplete="new-password" />

					<CustomValueInput
						label="Sylve Password (optional)"
						placeholder="Leave blank to use System Auth (PAM)"
						type="password"
						revealOnFocus={true}
						bind:value={password}
					/>

					<div class="space-y-1.5">
						<div class="flex items-center gap-2">
							<Checkbox id="import-admin" bind:checked={admin} />
							<Label for="import-admin" class="cursor-pointer text-sm">Admin</Label>
						</div>

						<p class="text-muted-foreground text-xs text-justify">
							Importing registers the existing Unix user in Sylve. The Unix password, groups, and
							home directory are left untouched. If you set a password, it is used only for Sylve
							authentication; otherwise the user must use System Auth (PAM).
						</p>
					</div>
				{/if}
			</div>
		{/if}

		<div class="flex justify-end gap-2 pt-1">
			<Button variant="outline" onclick={closeDialog}>Cancel</Button>
			<Button
				disabled={!selectedUsername.value ||
					submitting ||
					loadingUsers ||
					importableUsers.length === 0}
				onclick={submit}>{submitting ? 'Importing…' : 'Import'}</Button
			>
		</div>
	</Dialog.Content>
</Dialog.Root>
