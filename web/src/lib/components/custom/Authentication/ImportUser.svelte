<script lang="ts">
	import { importUser, listImportableUsers } from '$lib/api/auth/local';
	import Button from '$lib/components/ui/button/button.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import type { User } from '$lib/types/auth';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { watch } from 'runed';

	interface Props {
		open: boolean;
		reload?: boolean;
	}

	let { open = $bindable(), reload = $bindable() }: Props = $props();

	let importableUsers: User[] = $state([]);
	let loadingUsers = $state(false);
	let selectedUsername = $state({ open: false, value: '' });
	let password = $state('');
	let admin = $state(false);
	let submitting = $state(false);

	let userOptions = $derived(
		importableUsers.map((u) => ({
			value: u.username,
			label: `${u.username} (UID: ${u.uid}, ${u.homeDirectory})`
		}))
	);

	async function loadImportableUsers() {
		loadingUsers = true;
		try {
			importableUsers = await listImportableUsers();
		} catch {
			toast.error('Failed to load importable users', { position: 'bottom-center' });
			importableUsers = [];
		} finally {
			loadingUsers = false;
		}
	}

	function reset() {
		selectedUsername = { open: false, value: '' };
		password = '';
		admin = false;
		submitting = false;
	}

	watch(
		() => open,
		(value) => {
			if (value) {
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
		const error = validate();
		if (error) {
			toast.error(error, { position: 'bottom-center' });
			return;
		}

		submitting = true;
		try {
			const response = await importUser({
				username: selectedUsername.value,
				password: password || undefined,
				admin
			});

			reload = true;

			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to import user', { position: 'bottom-center' });
			} else {
				toast.success(`User "${selectedUsername.value}" imported`, {
					position: 'bottom-center'
				});
				open = false;
				reset();
			}
		} finally {
			submitting = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		onInteractOutside={() => {
			reset();
			open = false;
		}}
		class="lg:max-w-lg w-[92%] gap-4 p-5"
		showCloseButton={true}
		onClose={() => {
			reset();
			open = false;
		}}
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
				<p class="text-xs">All Unix users with UID &ge; 1000 are already registered in Sylve.</p>
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
					{@const info = importableUsers.find((u) => u.username === selectedUsername.value)}
					{#if info}
						<div class="bg-muted rounded-md p-3 text-sm space-y-1">
							<div class="flex justify-between">
								<span class="text-muted-foreground">UID:</span>
								<span>{info.uid || '-'}</span>
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
							Importing registers the existing Unix user in Sylve. The Unix user, its groups, and
							home directory are left untouched. If you set a password, the user can log in with
							Sylve credentials; otherwise they must use System Auth (PAM).
						</p>
					</div>
				{/if}
			</div>
		{/if}

		<div class="flex justify-end gap-2 pt-1">
			<Button
				variant="outline"
				onclick={() => {
					reset();
					open = false;
				}}>Cancel</Button
			>
			<Button
				disabled={!selectedUsername.value || submitting || importableUsers.length === 0}
				onclick={submit}>{submitting ? 'Importing…' : 'Import'}</Button
			>
		</div>
	</Dialog.Content>
</Dialog.Root>
