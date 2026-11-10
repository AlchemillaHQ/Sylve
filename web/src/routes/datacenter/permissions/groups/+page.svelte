<script lang="ts">
	import { deleteACLGroup, listACLGroups, setACLGroup } from '$lib/api/auth/permissions';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { loadPermissions } from '$lib/stores/permissions.svelte';
	import { subjectOf, type ACLGroup, type User } from '$lib/types/auth';
	import type { APIResponse } from '$lib/types/common';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { onMount, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	let { data }: { data: { groups: ACLGroup[]; users: User[]; loadErrors: APIResponse[] } } =
		$props();
	let groups = $state(untrack(() => data.groups));

	onMount(() => {
		for (const loadError of data.loadErrors) handleAPIError(loadError);
	});

	async function refresh() {
		const result = await listACLGroups();
		if (isAPIResponse(result)) {
			handleAPIError(result);
			return;
		}
		groups = result;
		await loadPermissions(true);
	}

	function groupForm(group?: ACLGroup) {
		return {
			open: false,
			editing: group !== undefined,
			name: group?.name ?? '',
			members: { open: false, value: [...(group?.members ?? [])] }
		};
	}

	let form = $state(groupForm());
	let deleteOpen = $state(false);
	let saving = $state(false);

	// PAM users of other nodes aren't listed here, so members already in the group stay as options.
	let memberOptions = $derived(
		[...new Set([...data.users.map(subjectOf), ...form.members.value])]
			.sort()
			.map((member) => ({ label: member, value: member }))
	);

	let tableData = $derived.by((): { rows: Row[]; columns: Column[] } => {
		const columns: Column[] = [
			{ field: 'id', title: 'ID', visible: false },
			{ field: 'name', title: 'Name' },
			{ field: 'members', title: 'Members' }
		];
		const rows: Row[] = groups.map((group) => ({
			id: group.name,
			name: group.name,
			members: group.members.join(', ') || 'None'
		}));
		return { columns, rows };
	});

	let query = $state('');
	let activeRows: Row[] | null = $state(null);
	let activeGroup = $derived(groups.find((group) => group.name === activeRows?.[0]?.id) ?? null);

	async function onSave() {
		const name = form.name.trim();
		if (!name) {
			toast.error('Name is required', { position: 'bottom-center' });
			return;
		}

		saving = true;
		try {
			const result = await setACLGroup(name, form.members.value);
			if (result.status !== 'success') {
				handleAPIError(result);
				toast.error('Failed to save group', { position: 'bottom-center' });
				return;
			}
			toast.success('Group saved', { position: 'bottom-center' });
			form = groupForm();
			await refresh();
		} finally {
			saving = false;
		}
	}
</script>

<div class="flex h-full flex-col overflow-hidden">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			onclick={() => (form = { ...groupForm(), open: true })}
			size="sm"
			class="h-6"
			disabled={saving}
		>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>
		{#if activeGroup}
			<Button
				onclick={() => (form = { ...groupForm(activeGroup), open: true })}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={saving}
			>
				<SpanWithIcon
					icon="icon-[material-symbols--edit]"
					size="h-4 w-4"
					gap="gap-2"
					title="Edit"
				/>
			</Button>
			<Button
				onclick={() => (deleteOpen = true)}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={saving}
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
	</div>

	<TreeTable
		data={tableData}
		name="tt-permission-groups"
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
		bind:query
	/>
</div>

{#if form.open}
	<Dialog.Root bind:open={form.open}>
		<Dialog.Content
			class="sm:max-w-120"
			onInteractOutside={(e) => e.preventDefault()}
			onEscapeKeydown={(e) => e.preventDefault()}
			showCloseButton={!saving}
			onClose={() => (form = groupForm())}
		>
			<Dialog.Header>
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[mdi--account-multiple]"
						size="h-5 w-5"
						gap="gap-2"
						title={form.editing ? `Edit ${form.name}` : 'New Group'}
					/>
				</Dialog.Title>
			</Dialog.Header>

			{#if !form.editing}
				<CustomValueInput
					label="Name"
					placeholder="operators"
					bind:value={form.name}
					classes="flex-1 space-y-1.5"
					disabled={saving}
				/>
			{/if}

			<CustomComboBox
				label="Members"
				placeholder="Select users"
				hint="Users of this node; a PAM user is the same person on every node with that name"
				bind:open={form.members.open}
				bind:value={form.members.value}
				data={memberOptions}
				multiple={true}
				width="w-full"
				disabled={saving}
			/>

			<Dialog.Footer class="flex justify-end">
				<Button onclick={() => onSave()} type="submit" size="sm" disabled={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						Saving...
					{:else}
						Save
					{/if}
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}

<AlertDialog
	open={deleteOpen}
	loading={saving}
	loadingLabel="Deleting..."
	keepOpenOnConfirm={true}
	customTitle={activeGroup
		? `Delete the group ${activeGroup.name}? Every grant to it is removed too.`
		: undefined}
	names={{ parent: 'group', element: activeGroup?.name || '' }}
	actions={{
		onConfirm: async () => {
			if (saving || !activeGroup) return;
			saving = true;
			try {
				const result = await deleteACLGroup(activeGroup.name);
				if (result.status !== 'success') {
					handleAPIError(result);
					toast.error('Failed to delete group', { position: 'bottom-center' });
					return;
				}
				toast.success('Group deleted', { position: 'bottom-center' });
				activeRows = null;
				deleteOpen = false;
				await refresh();
			} finally {
				saving = false;
			}
		},
		onCancel: () => (deleteOpen = false)
	}}
></AlertDialog>
