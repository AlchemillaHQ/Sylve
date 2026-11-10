<script lang="ts">
	import { deleteUser, editUser, listUsers } from '$lib/api/auth/local';
	import LocalUserForm from '$lib/components/custom/Authentication/LocalUserForm.svelte';
	import Passkeys from '$lib/components/custom/Authentication/Passkeys.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { User } from '$lib/types/auth';
	import type { APIResponse } from '$lib/types/common';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { escapeHTML } from '$lib/utils/string';
	import { renderBadge } from '$lib/utils/table';
	import { convertDbTime, getLastUsage } from '$lib/utils/time';
	import { watch } from 'runed';
	import { onMount, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	let {
		data
	}: {
		data: {
			node: string;
			users: User[];
			realmLabels: Record<string, string>;
			loadErrors: APIResponse[];
		};
	} = $props();
	let users = $state(untrack(() => data.users));

	onMount(() => {
		for (const loadError of data.loadErrors) handleAPIError(loadError);
	});

	let reload = $state(false);
	let deleting = $state(false);

	async function refreshUsers() {
		const [local, realm] = await Promise.all([
			listUsers('local', { hostname: data.node }),
			listUsers('oidc', { hostname: data.node })
		]);
		if (isAPIResponse(local) || isAPIResponse(realm)) {
			handleAPIError(isAPIResponse(local) ? local : (realm as APIResponse));
			return;
		}
		users = [...local, ...realm];
	}

	watch(
		() => reload,
		(value) => {
			if (!value) return;
			reload = false;
			void refreshUsers();
		}
	);

	async function toggleLock(user: User) {
		const result = await editUser(
			user.id,
			{ username: user.username, locked: !user.locked },
			{ hostname: data.node }
		);
		if (isAPIResponse(result)) {
			handleAPIError(result);
			toast.error('Failed to update user', { position: 'bottom-center' });
			return;
		}
		reload = true;
	}

	function scope(user: User): string {
		if (user.shared || user.source === 'oidc') return 'Cluster';
		return user.nameTaken ? `${data.node} only, name taken` : `${data.node} only`;
	}

	function realmLabel(user: User): string {
		if (user.source !== 'oidc') return '';
		const realm = user.username.slice(user.username.lastIndexOf('@') + 1);
		return data.realmLabels[realm] ?? realm;
	}

	let tableData = $derived.by((): { rows: Row[]; columns: Column[] } => {
		const columns: Column[] = [
			{ field: 'id', title: 'ID', visible: false },
			{ field: 'name', title: 'Name' },
			{
				field: 'scope',
				title: 'Scope',
				formatter: (cell: CellComponent) => {
					const { realm, locked } = cell.getRow().getData();
					const badges = [
						realm ? renderBadge(realm, 'text-sky-400 border-sky-400/50') : '',
						locked ? renderBadge('LOCKED', 'text-red-400 border-red-400/50') : ''
					].join('');
					return `<span class="inline-flex items-baseline gap-1.5"><span>${escapeHTML(cell.getValue())}</span><span class="inline-flex translate-y-px gap-1.5">${badges}</span></span>`;
				}
			},
			{
				field: 'email',
				title: 'E-Mail',
				// A realm user's email comes from their provider, so it's escaped like any remote text.
				formatter: (cell: CellComponent) => escapeHTML(cell.getValue() || '-')
			},
			{
				field: 'lastUsage',
				title: 'Last Usage',
				formatter: (cell: CellComponent) => getLastUsage(cell.getValue())
			},
			{ field: 'createdAt', title: 'Created At' }
		];
		const rows: Row[] = users.map((user) => ({
			id: user.id,
			name: user.username,
			scope: scope(user),
			realm: realmLabel(user),
			locked: user.locked,
			email: user.email,
			lastUsage: user.lastLoginTime ? convertDbTime(user.lastLoginTime) : 'Never',
			createdAt: user.createdAt ? convertDbTime(user.createdAt) : 'Never'
		}));
		return { rows, columns };
	});

	let query = $state('');
	let activeRows: Row[] | null = $state(null);
	let activeUser = $derived(users.find((user) => user.id === activeRows?.[0]?.id));
	let protectedUser = $derived(activeUser?.username === 'admin' || activeUser?.username === 'root');

	let modals = $state({
		create: { open: false },
		delete: { open: false },
		edit: { open: false },
		passkeys: { open: false }
	});
</script>

<div class="flex h-full flex-col overflow-hidden">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button onclick={() => (modals.create.open = true)} size="sm" class="h-6">
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>

		{#if activeUser}
			{#if activeUser.source === 'oidc'}
				<Button
					onclick={() => activeUser && toggleLock(activeUser)}
					size="sm"
					variant="outline"
					class="h-6.5"
				>
					<SpanWithIcon
						icon={activeUser.locked ? 'icon-[mdi--lock-open]' : 'icon-[mdi--lock]'}
						size="h-4 w-4"
						gap="gap-2"
						title={activeUser.locked ? 'Unlock' : 'Lock'}
					/>
				</Button>
			{:else}
				<Button onclick={() => (modals.edit.open = true)} size="sm" variant="outline" class="h-6.5">
					<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
				</Button>
			{/if}
			<Button
				onclick={() => (modals.delete.open = true)}
				size="sm"
				variant="outline"
				class="h-6.5 pointer-events-auto!"
				disabled={deleting || protectedUser}
				title={protectedUser ? 'Cannot delete this user' : ''}
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
			{#if activeUser.source !== 'oidc'}
				<Button
					onclick={() => (modals.passkeys.open = true)}
					size="sm"
					variant="outline"
					class="h-6.5"
					title={activeUser.passkeyEligible
						? 'Passkeys'
						: 'View existing passkeys; registration is unavailable for this user'}
				>
					<SpanWithIcon
						icon="icon-[mdi--fingerprint]"
						size="h-4 w-4"
						gap="gap-2"
						title="Passkeys"
					/>
				</Button>
			{/if}
		{/if}
	</div>

	<TreeTable
		data={tableData}
		name="tt-permission-users"
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
		bind:query
	/>
</div>

{#if modals.create.open}
	<LocalUserForm bind:open={modals.create.open} {users} hostname={data.node} bind:reload />
{/if}

{#if modals.edit.open && activeUser}
	<LocalUserForm
		bind:open={modals.edit.open}
		{users}
		edit={true}
		user={activeUser}
		hostname={data.node}
		bind:reload
	/>
{/if}

{#if modals.passkeys.open && activeUser}
	<Passkeys
		bind:open={modals.passkeys.open}
		userId={activeUser.id}
		username={activeUser.username}
		hostname={data.node}
		registrationEligible={activeUser.passkeyEligible}
		bind:reload
	/>
{/if}

<AlertDialog
	bind:open={modals.delete.open}
	loading={deleting}
	loadingLabel="Deleting..."
	keepOpenOnConfirm={true}
	names={{ parent: 'User', element: activeUser?.username ?? '' }}
	actions={{
		onConfirm: async () => {
			if (deleting || !activeUser) return;
			deleting = true;
			try {
				const response = await deleteUser(activeUser.id, false, { hostname: data.node });
				if (isAPIResponse(response)) {
					handleAPIError(response);
					toast.error('Failed to delete user', { position: 'bottom-center' });
					return;
				}
				reload = true;
				activeRows = null;
				modals.delete.open = false;
				toast.success('User deleted', { position: 'bottom-center' });
			} finally {
				deleting = false;
			}
		},
		onCancel: () => (modals.delete.open = false)
	}}
/>
