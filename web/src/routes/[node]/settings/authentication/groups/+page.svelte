<script lang="ts">
	import { createGroup, deleteGroup, listGroups, updateGroupMembers } from '$lib/api/auth/groups';
	import { listUsers } from '$lib/api/auth/local';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Group, User } from '$lib/types/auth';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';

	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		users: User[];
		groups: Group[];
	}

	let { data }: { data: Data } = $props();

	const users = resource(
		() => 'users',
		async (key, prevKey, { signal }) => {
			const res = await listUsers();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.users }
	);

	let groups = resource(
		() => 'groups',
		async (key, prevKey, { signal }) => {
			const res = await listGroups();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.groups }
	);

	let usersOptions = $derived.by(() => {
		return users.current.map((user) => ({
			label: user.username,
			value: user.username
		}));
	});

	let options = {
		create: {
			open: false,
			name: '',
			users: {
				open: false,
				value: [] as string[],
				data: (() => $state.snapshot(usersOptions))()
			}
		},
		delete: {
			open: false,
			id: 0
		},
		modifyUsers: {
			open: false,
			combobox: {
				open: false,
				value: [] as string[],
				data: (() => $state.snapshot(usersOptions))()
			}
		}
	};

	let properties = $state(options);
	let reload = $state(false);

	watch(
		() => reload,
		(current) => {
			if (current) {
				groups.refetch();
				users.refetch();
				reload = false;
			}
		}
	);

	async function onCreate() {
		let error = '';

		if (!properties.create.name.trim() || properties.create.users.value.length === 0) {
			error = 'Name and users are required';
		} else if (groups.current.some((g) => g.name === properties.create.name.trim())) {
			error = 'Group name already exists';
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
		}

		const response = await createGroup(
			properties.create.name.trim(),
			properties.create.users.value
		);

		reload = true;

		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to create group', {
				position: 'bottom-center'
			});
			return;
		} else {
			toast.success('Group created', {
				position: 'bottom-center'
			});

			properties.create.open = false;
			properties.create.name = '';
			properties.create.users.value = [];
		}
	}

	async function onModifyUsers() {
		const response = await updateGroupMembers(
			properties.modifyUsers.combobox.value,
			activeRow ? activeRow.name : ''
		);

		reload = true;

		if (response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to modify users in group', {
				position: 'bottom-center'
			});
			return;
		} else {
			toast.success('Users modified in group', {
				position: 'bottom-center'
			});

			properties.modifyUsers.open = false;
			properties.modifyUsers.combobox.value = [];
		}
	}

	function generateTableData(users: User[], groups: Group[]): { rows: Row[]; columns: Column[] } {
		const columns: Column[] = [
			{
				field: 'id',
				title: 'ID',
				visible: false
			},
			{
				field: 'name',
				title: 'Name',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					if (value === 'sylve_g') {
						return `Default Sylve Group (sylve_g)`;
					}

					return value;
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

		const rows: Row[] = [];

		for (const group of groups) {
			rows.push({
				id: group.id,
				name: group.name,
				createdAt: group.createdAt,
				user: false,
				children: group.users?.map((user) => ({
					id: user.id,
					name: user.username,
					createdAt: user.createdAt,
					user: true
				}))
			});
		}

		return {
			columns,
			rows
		};
	}

	let tableData = $derived(generateTableData(users.current, groups.current));
	let query: string = $state('');
	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
</script>

{#snippet button(type: string)}
	{#if activeRows && activeRows.length === 1 && !activeRows[0].user}
		{#if type === 'delete'}
			{#if activeRow?.name !== 'sylve_g'}
				<Button
					onclick={() => {
						properties.delete.open = !properties.delete.open;
						properties.delete.id = activeRows ? (activeRows[0].id as number) : 0;
					}}
					size="sm"
					variant="outline"
					class="h-6.5"
				>
					<div class="flex items-center">
						<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
						<span>Delete</span>
					</div>
				</Button>
			{/if}
		{/if}

		{#if type === 'modify-users'}
			<Button
				onclick={() => {
					properties.modifyUsers.open = !properties.modifyUsers.open;
					if (activeRows) {
						properties.modifyUsers.combobox.value =
							activeRows[0].children?.map((user) => user.name) || [];
					}
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[material-symbols--edit] mr-1 h-4 w-4"></span>

					<span>Edit Users</span>
				</div>
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full flex-col overflow-hidden">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			onclick={() => (properties.create.open = !properties.create.open)}
			size="sm"
			class="h-6"
		>
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>New</span>
			</div>
		</Button>

		{@render button('modify-users')}
		{@render button('delete')}
	</div>

	<TreeTable
		data={tableData}
		name={'tt-groups'}
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
		bind:query
	/>
</div>

{#if properties.create.open}
	<Dialog.Root bind:open={properties.create.open}>
		<Dialog.Content
			class="sm:max-w-106.25"
			onInteractOutside={(e) => e.preventDefault()}
			onEscapeKeydown={(e) => e.preventDefault()}
		>
			<Dialog.Header>
				<Dialog.Title class="flex items-center justify-between">
					<div class="flex items-center gap-2">
						<span class="icon-[mdi--account-group] h-5 w-5"></span>

						<span>New Group</span>
					</div>
					<div class="flex items-center gap-0.5">
						<Button
							size="sm"
							variant="link"
							title={'Reset'}
							class="h-4"
							onclick={() => {
								properties = options;
							}}
						>
							<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
							<span class="sr-only">{'Reset'}</span>
						</Button>
						<Button
							size="sm"
							variant="link"
							class="h-4"
							title={'Close'}
							onclick={() => {
								properties = options;
								properties.create.open = false;
							}}
						>
							<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"
							></span>
							<span class="sr-only">{'Close'}</span>
						</Button>
					</div>
				</Dialog.Title>
			</Dialog.Header>

			<CustomValueInput
				label={'Name'}
				placeholder="c-level"
				bind:value={properties.create.name}
				classes="flex-1 space-y-1.5"
			/>

			<CustomComboBox
				bind:open={properties.create.users.open}
				bind:value={properties.create.users.value}
				data={properties.create.users.data}
				onValueChange={(v) => {
					properties.create.users.value = v as string[];
				}}
				placeholder={'Select users'}
				multiple={true}
				width="w-full"
			/>

			<Dialog.Footer class="flex justify-end">
				<div class="flex w-full items-center justify-end gap-2">
					<Button onclick={() => onCreate()} type="submit" size="sm">{'Create'}</Button>
				</div>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}

{#if properties.modifyUsers.open}
	<Dialog.Root bind:open={properties.modifyUsers.open}>
		<Dialog.Content
			class="sm:max-w-106.25"
			onInteractOutside={(e) => e.preventDefault()}
			onEscapeKeydown={(e) => e.preventDefault()}
		>
			<Dialog.Header>
				<Dialog.Title class="flex items-center justify-between">
					<div class="flex items-center gap-2">
						<span class="icon-[material-symbols--edit] h-5 w-5"></span>
						<span>Edit Users</span>
					</div>
					<div class="flex items-center gap-0.5">
						<Button
							size="sm"
							variant="link"
							title={'Reset'}
							class="h-4"
							onclick={() => {
								properties = options;
							}}
						>
							<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
							<span class="sr-only">{'Reset'}</span>
						</Button>
						<Button
							size="sm"
							variant="link"
							class="h-4"
							title={'Close'}
							onclick={() => {
								properties = options;
								properties.modifyUsers.open = false;
							}}
						>
							<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"
							></span>
							<span class="sr-only">{'Close'}</span>
						</Button>
					</div>
				</Dialog.Title>
			</Dialog.Header>

			<CustomComboBox
				bind:open={properties.modifyUsers.combobox.open}
				bind:value={properties.modifyUsers.combobox.value}
				data={properties.modifyUsers.combobox.data}
				onValueChange={(v) => {
					properties.modifyUsers.combobox.value = v as string[];
				}}
				placeholder={'Select users'}
				multiple={true}
				width="w-full"
			/>

			<Dialog.Footer class="flex justify-end">
				<div class="flex w-full items-center justify-end gap-2">
					<Button onclick={() => onModifyUsers()} type="submit" size="sm">{'Modify Users'}</Button>
				</div>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}

<AlertDialog
	open={properties.delete.open}
	names={{ parent: 'group', element: activeRow?.name || '' }}
	actions={{
		onConfirm: async () => {
			const result = await deleteGroup(properties.delete.id);
			reload = true;
			activeRows = null;
			activeRow = null;
			if (result.status === 'error') {
				handleAPIError(result);
				toast.error('Failed to delete group', {
					position: 'bottom-center'
				});
				return;
			} else {
				toast.success('Group deleted', {
					position: 'bottom-center'
				});

				properties.delete.open = false;
				properties.delete.id = 0;
			}
		},
		onCancel: () => {
			properties.delete.open = false;
			properties.delete.id = 0;
		}
	}}
></AlertDialog>
