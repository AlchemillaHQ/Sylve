<script lang="ts">
	import { createGroup, deleteGroup, listGroups, updateGroupMembers } from '$lib/api/auth/groups';
	import { listUsers } from '$lib/api/auth/local';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { APIResponse } from '$lib/types/common';
	import type { Group, User } from '$lib/types/auth';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import {
		handleAPIError,
		isAPIResponse,
		isRequestCancellation,
		updateCache
	} from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';

	import { resource, watch } from 'runed';
	import { onDestroy, onMount, untrack } from 'svelte';
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
	let pageActive = true;
	onDestroy(() => {
		pageActive = false;
	});

	const lastUsersByNode: Record<string, User[]> = Object.create(null);
	lastUsersByNode[initialData.node] = initialData.users;
	const users = resource(
		() => data.node,
		async (node, _previousNode, { signal }) => {
			try {
				const result = await listUsers(undefined, { hostname: node, signal });
				if (isAPIResponse(result)) {
					handleAPIError(result);
					return lastUsersByNode[node] ?? [];
				}
				lastUsersByNode[node] = result;
				await updateCache('users', result, node);
				return result;
			} catch (error) {
				if (isRequestCancellation(error)) return lastUsersByNode[node] ?? [];
				throw error;
			}
		},
		{ initialValue: initialData.users }
	);

	const lastGroupsByNode: Record<string, Group[]> = Object.create(null);
	lastGroupsByNode[initialData.node] = initialData.groups;
	let groups = resource(
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
		{ initialValue: initialData.groups }
	);

	onMount(() => {
		for (const loadError of initialData.loadErrors) handleAPIError(loadError);
	});

	let usersOptions = $derived.by(() => {
		return users.current
			.filter((user) => user.source === 'pam')
			.map((user) => ({
				label: user.username,
				value: user.username
			}));
	});

	function defaultProperties() {
		return {
			create: {
				open: false,
				name: '',
				users: {
					open: false,
					value: [] as string[]
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
					value: [] as string[]
				}
			}
		};
	}

	let properties = $state(defaultProperties());
	let reload = $state(false);
	let creating = $state(false);
	let updating = $state(false);
	let deleting = $state(false);

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
		if (creating) return;
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

		creating = true;
		const hostname = data.node;
		const name = properties.create.name.trim();
		const members = [...properties.create.users.value];
		try {
			const response = await createGroup(name, members, { hostname });
			if (!pageActive || data.node !== hostname || !properties.create.open) return;

			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Failed to create group', {
					position: 'bottom-center'
				});
				return;
			}

			toast.success('Group created', {
				position: 'bottom-center'
			});
			if (data.node !== hostname) return;

			reload = true;
			properties.create.open = false;
			properties.create.name = '';
			properties.create.users.value = [];
		} finally {
			creating = false;
		}
	}

	async function onModifyUsers() {
		if (updating || !activeGroup) return;

		updating = true;
		const groupID = activeGroup.id;
		const hostname = data.node;
		const usernames = withRequiredMembers(properties.modifyUsers.combobox.value, activeGroup);
		try {
			const response = await updateGroupMembers(groupID, usernames, { hostname });
			if (
				!pageActive ||
				data.node !== hostname ||
				activeGroup?.id !== groupID ||
				!properties.modifyUsers.open
			)
				return;

			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Failed to modify users in group', {
					position: 'bottom-center'
				});
				return;
			}

			toast.success('Users modified in group', {
				position: 'bottom-center'
			});
			if (data.node !== hostname) return;

			reload = true;
			properties.modifyUsers.open = false;
			properties.modifyUsers.combobox.value = [];
		} finally {
			updating = false;
		}
	}

	function generateTableData(groups: Group[]): { rows: Row[]; columns: Column[] } {
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

	let tableData = $derived(generateTableData(groups.current));
	let query: string = $state('');
	let activeRows: Row[] | null = $state(null);
	let activeGroup: Group | null = $derived.by(() => {
		const row = activeRows?.[0];
		if (!row) return null;
		return groups.current.find((group) => group.id === Number(row.id)) ?? null;
	});

	function requiredMembers(group: Group): string[] {
		const members = (group.users ?? [])
			.filter(
				(user) =>
					user.source === 'pam' &&
					((group.name === 'wheel' && user.username === 'root') ||
						user.primaryGroupId === group.id ||
						(group.name === 'sylve_g' && user.primaryGroupId == null))
			)
			.map((user) => user.username);
		if (group.name === 'wheel' && !members.includes('root')) members.push('root');
		return members;
	}

	function editableMembers(group: Group): string[] {
		return (group.users ?? []).filter((user) => user.source === 'pam').map((user) => user.username);
	}

	function withRequiredMembers(usernames: string[], group: Group): string[] {
		return Array.from(new Set([...usernames, ...requiredMembers(group)]));
	}

	let activeRequiredMembers = $derived(activeGroup ? requiredMembers(activeGroup) : []);

	watch(
		() => data.node,
		() => {
			activeRows = null;
			properties = defaultProperties();
		}
	);
</script>

{#snippet button(type: string)}
	{#if activeRows && activeRows.length === 1 && !activeRows[0].user}
		{#if type === 'delete'}
			<Button
				onclick={() => {
					properties.delete.open = !properties.delete.open;
					properties.delete.id = activeRows ? (activeRows[0].id as number) : 0;
				}}
				size="sm"
				variant="outline"
				class="h-6.5 pointer-events-auto!"
				disabled={deleting ||
					!activeGroup ||
					activeGroup.name === 'sylve_g' ||
					activeGroup.name === 'wheel'}
				title={activeGroup?.name === 'sylve_g'
					? 'Default system group, cannot be deleted'
					: activeGroup?.name === 'wheel'
						? 'System group, cannot be deleted'
						: 'Delete'}
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}

		{#if type === 'modify-users'}
			<Button
				onclick={() => {
					properties.modifyUsers.open = !properties.modifyUsers.open;
					if (activeGroup) {
						properties.modifyUsers.combobox.value = editableMembers(activeGroup);
					}
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={updating}
			>
				<SpanWithIcon
					icon="icon-[material-symbols--edit]"
					size="h-4 w-4"
					gap="gap-2"
					title="Edit Users"
				/>
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
			disabled={creating}
		>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>

		{@render button('modify-users')}
		{@render button('delete')}
	</div>

	<TreeTable
		data={tableData}
		name="tt-groups"
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
			showResetButton={!creating}
			showCloseButton={!creating}
			onClose={() => {
				properties = defaultProperties();
				properties.create.open = false;
			}}
			onReset={() => {
				properties.create.name = '';
				properties.create.users.value = [];
				properties.create.users.open = false;
			}}
		>
			<Dialog.Header>
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[mdi--account-group]"
						size="h-5 w-5"
						gap="gap-2"
						title="New Group"
					/>
				</Dialog.Title>
			</Dialog.Header>

			<CustomValueInput
				label="Name"
				placeholder="c-level"
				bind:value={properties.create.name}
				classes="flex-1 space-y-1.5"
				disabled={creating}
			/>

			<CustomComboBox
				bind:open={properties.create.users.open}
				bind:value={properties.create.users.value}
				data={usersOptions}
				onValueChange={(v) => {
					properties.create.users.value = v as string[];
				}}
				placeholder="Select Users"
				multiple={true}
				width="w-full"
				disabled={creating}
			/>

			<Dialog.Footer class="flex justify-end">
				<div class="flex w-full items-center justify-end gap-2">
					<Button onclick={() => onCreate()} type="submit" size="sm" disabled={creating}>
						{#if creating}
							<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
							Creating...
						{:else}
							Create
						{/if}
					</Button>
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
			showCloseButton={!updating}
			showResetButton={!updating}
			onClose={() => {
				properties = defaultProperties();
				properties.modifyUsers.open = false;
			}}
			onReset={() => {
				properties.modifyUsers.combobox.value = activeGroup ? editableMembers(activeGroup) : [];
				properties.modifyUsers.combobox.open = false;
			}}
		>
			<Dialog.Header>
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[material-symbols--edit]"
						size="h-5 w-5"
						gap="gap-2"
						title="Edit Users"
					/>
				</Dialog.Title>
			</Dialog.Header>

			<CustomComboBox
				bind:open={properties.modifyUsers.combobox.open}
				bind:value={properties.modifyUsers.combobox.value}
				data={usersOptions}
				onValueChange={(v) => {
					properties.modifyUsers.combobox.value = activeGroup
						? withRequiredMembers(v as string[], activeGroup)
						: (v as string[]);
				}}
				placeholder="Select Users"
				multiple={true}
				width="w-full"
				disabled={updating}
			/>

			{#if activeRequiredMembers.length > 0}
				<p class="text-muted-foreground text-xs text-justify">
					Primary group members{activeGroup?.name === 'wheel' ? ', including root,' : ''} cannot be removed
					here. This restriction is enforced by the system.
				</p>
			{/if}

			<Dialog.Footer class="flex justify-end">
				<div class="flex w-full items-center justify-end gap-2">
					<Button onclick={() => onModifyUsers()} type="submit" size="sm" disabled={updating}>
						{#if updating}
							<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
							Saving...
						{:else}
							Modify Users
						{/if}
					</Button>
				</div>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}

<AlertDialog
	open={properties.delete.open}
	loading={deleting}
	loadingLabel="Deleting..."
	keepOpenOnConfirm={true}
	names={{ parent: 'group', element: activeGroup?.name || '' }}
	actions={{
		onConfirm: async () => {
			if (deleting) return;
			deleting = true;
			const hostname = data.node;
			const groupID = properties.delete.id;
			try {
				const result = await deleteGroup(groupID, { hostname });
				if (!pageActive || data.node !== hostname || properties.delete.id !== groupID) return;
				if (isAPIResponse(result)) {
					handleAPIError(result);
					toast.error('Failed to delete group', {
						position: 'bottom-center'
					});
					return;
				}

				toast.success('Group deleted', {
					position: 'bottom-center'
				});
				reload = true;
				activeRows = null;
				properties.delete.open = false;
				properties.delete.id = 0;
			} finally {
				deleting = false;
			}
		},
		onCancel: () => {
			properties.delete.open = false;
			properties.delete.id = 0;
		}
	}}
></AlertDialog>
