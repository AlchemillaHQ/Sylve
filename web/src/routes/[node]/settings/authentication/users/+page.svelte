<script lang="ts">
	import { deleteUser, listUsers } from '$lib/api/auth/local';
	import CreateOrEdit from '$lib/components/custom/Authentication/CreateOrEdit.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { User } from '$lib/types/auth';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { convertDbTime, getLastUsage } from '$lib/utils/time';
	import { createQuery } from '@tanstack/svelte-query';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		users: User[];
	}

	let { data }: { data: Data } = $props();

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
			lastUsage: user.lastLoginTime ? convertDbTime(user.lastLoginTime) : 'Never',
			createdAt: user.createdAt ? convertDbTime(user.createdAt) : 'Never'
		}));

		return { rows, columns };
	}

	const results = createQuery(() => ({
		queryKey: ['users'],
		queryFn: async () => {
			return (await listUsers()) as User[];
		},
		keepPreviousData: true,
		initialData: data.users,
		onSuccess: (data: User[]) => {
			updateCache('users', data);
		}
	}));

	let reload = $state(false);

	$effect(() => {
		if (reload) {
			results.refetch();
			reload = false;
		}
	});

	let users: User[] = $derived(results.data as User[]);
	let tableData = $derived(generateTableData(users));
	let query: string = $state('');
	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));

	let modals = $state({
		create: { open: false },
		delete: { open: false },
		edit: { open: false }
	});
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
				class="h-6.5 !pointer-events-auto"
				disabled={!activeRow || activeRow.name === 'admin'}
				title={activeRow && activeRow.name === 'admin' ? 'Cannot delete admin user' : ''}
			>
				<div class="flex items-center">
					<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>

					<span>Delete</span>
				</div>
			</Button>
		{/if}

		{#if type === 'edit'}
			<Button
				onclick={() => {
					modals.edit.open = !modals.edit.open;
				}}
				size="sm"
				variant="outline"
				class="h-6.5 !pointer-events-auto"
				disabled={!activeRow || activeRow.name === 'admin'}
				title={activeRow && activeRow.name === 'admin' ? 'Cannot edit admin user' : ''}
			>
				<div class="flex items-center">
					<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>
					<span>Edit</span>
				</div>
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full flex-col overflow-hidden">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button onclick={() => (modals.create.open = !modals.create.open)} size="sm" class="h-6">
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>New</span>
			</div>
		</Button>

		{@render button('edit')}
		{@render button('delete')}
	</div>

	<TreeTable
		data={tableData}
		name={'tt-users'}
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
		bind:query
	/>
</div>

{#if modals.create.open}
	<CreateOrEdit bind:open={modals.create.open} {users} bind:reload />
{/if}

{#if modals.edit.open}
	<CreateOrEdit
		bind:open={modals.edit.open}
		{users}
		edit={true}
		user={activeRow ? (users.find((u) => u.id === activeRow.id) as User) : undefined}
		bind:reload
	/>
{/if}

<AlertDialog
	bind:open={modals.delete.open}
	names={{
		parent: 'User',
		element: activeRow ? (activeRow.name as string) : ''
	}}
	actions={{
		onConfirm: async () => {
			const response = await deleteUser(activeRow.id as string);
			reload = true;
			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to delete user', {
					position: 'bottom-center'
				});
			} else {
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
