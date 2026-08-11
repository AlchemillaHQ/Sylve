<script lang="ts">
	import { listGroups } from '$lib/api/auth/groups';
	import { deleteUser, listUsers } from '$lib/api/auth/local';
	import PamUserForm from '$lib/components/custom/Authentication/PamUserForm.svelte';
	import ImportUser from '$lib/components/custom/Authentication/ImportUser.svelte';
	import Passkeys from '$lib/components/custom/Authentication/Passkeys.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Group, User } from '$lib/types/auth';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import {
		handleAPIError,
		isAPIResponse,
		isRequestCancellation,
		updateCache
	} from '$lib/utils/http';
	import { convertDbTime, getLastUsage } from '$lib/utils/time';
	import { resource, watch } from 'runed';
	import { onMount, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		node: string;
		users: User[];
		groups: Group[];
		loadErrors: APIResponse[];
	}

	let { data }: { data: Data } = $props();
	const initialData = untrack(() => data);

	function generateTableData(users: User[]): { rows: Row[]; columns: Column[] } {
		const columns: Column[] = [
			{ field: 'id', title: 'ID', visible: false },
			{ field: 'name', title: 'Name' },
			{
				field: 'email',
				title: 'E-Mail',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					return value ? value : '-';
				}
			},
			{
				field: 'uid',
				title: 'UID',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					return value ? String(value) : '-';
				}
			},
			{
				field: 'lastUsage',
				title: 'Last Usage',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					return getLastUsage(value);
				}
			},
			{
				field: 'createdAt',
				title: 'Created At',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					return convertDbTime(value);
				}
			}
		];

		const rows: Row[] = users.map((user) => ({
			id: user.id,
			name: user.username,
			email: user.email,
			uid: user.uid,
			lastUsage: user.lastLoginTime ? convertDbTime(user.lastLoginTime) : 'Never',
			createdAt: user.createdAt ? convertDbTime(user.createdAt) : 'Never'
		}));

		return { rows, columns };
	}

	const lastUsersByNode: Record<string, User[]> = Object.create(null);
	lastUsersByNode[initialData.node] = initialData.users;
	const users = resource(
		() => data.node,
		async (node, _previousNode, { signal }) => {
			try {
				const result = await listUsers('pam', { hostname: node, signal });
				if (isAPIResponse(result)) {
					handleAPIError(result);
					return lastUsersByNode[node] ?? [];
				}
				lastUsersByNode[node] = result;
				await updateCache('users_pam', result, node);
				return result;
			} catch (error) {
				if (isRequestCancellation(error)) return lastUsersByNode[node] ?? [];
				throw error;
			}
		},
		{
			initialValue: initialData.users
		}
	);

	const lastGroupsByNode: Record<string, Group[]> = Object.create(null);
	lastGroupsByNode[initialData.node] = initialData.groups;
	const groups = resource(
		() => data.node,
		async (node, _previousNode, { signal }) => {
			try {
				const result = await listGroups({ hostname: node, signal });
				if (isAPIResponse(result)) {
					handleAPIError(result);
					return lastGroupsByNode[node] ?? [];
				}
				lastGroupsByNode[node] = result;
				await updateCache('groups', result, node);
				return result;
			} catch (error) {
				if (isRequestCancellation(error)) return lastGroupsByNode[node] ?? [];
				throw error;
			}
		},
		{
			initialValue: initialData.groups
		}
	);

	onMount(() => {
		for (const loadError of initialData.loadErrors) handleAPIError(loadError);
	});

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (value) {
				users.refetch();
				groups.refetch();
				reload = false;
			}
		}
	);

	let tableData = $derived(generateTableData(users.current));
	let query: string = $state('');
	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let activeUser: User | undefined = $derived(
		activeRow ? users.current.find((user) => user.id === activeRow.id) : undefined
	);

	let modals = $state({
		create: { open: false },
		delete: { open: false },
		edit: { open: false },
		passkeys: { open: false },
		import: { open: false }
	});

	watch(
		() => data.node,
		() => {
			activeRows = null;
			modals.create.open = false;
			modals.delete.open = false;
			modals.edit.open = false;
			modals.passkeys.open = false;
			modals.import.open = false;
		}
	);
</script>

{#snippet button(type: string)}
	{#if activeRows && activeRows.length === 1}
		{#if type === 'delete'}
			<Button
				onclick={() => {
					modals.delete.open = !modals.delete.open;
				}}
				size="sm"
				variant="outline"
				class="h-6.5 pointer-events-auto!"
				disabled={!activeRow || activeRow.name === 'root'}
				title={activeRow && activeRow.name === 'root' ? 'Cannot delete this user' : ''}
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}

		{#if type === 'edit'}
			<Button
				onclick={() => {
					modals.edit.open = !modals.edit.open;
				}}
				size="sm"
				variant="outline"
				class="h-6.5 pointer-events-auto!"
			>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
			</Button>
		{/if}

		{#if type === 'passkeys'}
			<Button
				onclick={() => {
					modals.passkeys.open = !modals.passkeys.open;
				}}
				size="sm"
				variant="outline"
				class="h-6.5 pointer-events-auto!"
				title={activeUser?.passkeyEligible
					? 'Passkeys'
					: 'View existing passkeys; registration is unavailable for this user'}
			>
				<SpanWithIcon icon="icon-[mdi--fingerprint]" size="h-4 w-4" gap="gap-2" title="Passkeys" />
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full flex-col overflow-hidden">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button onclick={() => (modals.create.open = !modals.create.open)} size="sm" class="h-6">
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>

		{@render button('delete')}
		{@render button('edit')}
		{@render button('passkeys')}

		<Button
			onclick={() => (modals.import.open = !modals.import.open)}
			size="sm"
			class="h-6 ml-auto"
		>
			<SpanWithIcon icon="icon-[mdi--import]" size="h-4 w-4" gap="gap-2" title="Import" />
		</Button>
	</div>

	<TreeTable
		data={tableData}
		name="tt-users-pam"
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
		bind:query
	/>
</div>

{#if modals.create.open}
	<PamUserForm
		bind:open={modals.create.open}
		users={users.current}
		groups={groups.current}
		edit={false}
		hostname={data.node}
		bind:reload
	/>
{/if}

{#if modals.edit.open}
	<PamUserForm
		bind:open={modals.edit.open}
		users={users.current}
		groups={groups.current}
		user={activeUser}
		hostname={data.node}
		bind:reload
	/>
{/if}

{#if modals.passkeys.open && activeRow}
	<Passkeys
		bind:open={modals.passkeys.open}
		userId={activeRow.id as number}
		username={String(activeRow.name || '')}
		hostname={data.node}
		registrationEligible={activeUser?.passkeyEligible ?? false}
		bind:reload
	/>
{/if}

{#if modals.import.open}
	<ImportUser bind:open={modals.import.open} hostname={data.node} bind:reload />
{/if}

<AlertDialog
	bind:open={modals.delete.open}
	customTitle="This action cannot be undone. It permanently removes the managed Unix account and its home directory, along with the Sylve user record."
	names={{
		parent: 'User',
		element: activeRow ? (activeRow.name as string) : ''
	}}
	actions={{
		onConfirm: async () => {
			const response = await deleteUser(Number(activeRow.id), { hostname: data.node });
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Failed to delete user', {
					position: 'bottom-center'
				});
			} else {
				reload = true;
				toast.success('User deleted', {
					position: 'bottom-center'
				});
			}

			modals.delete.open = false;
		},
		onCancel: () => {
			modals.delete.open = false;
		}
	}}
/>
