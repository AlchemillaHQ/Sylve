<script lang="ts">
	import { deleteRole, listRoles, setRole } from '$lib/api/auth/permissions';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import RoleGuide from '$lib/components/custom/Permissions/RoleGuide.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { loadPermissions } from '$lib/stores/permissions.svelte';
	import type { Role } from '$lib/types/auth';
	import type { APIResponse } from '$lib/types/common';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { onMount, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	let { data }: { data: { roles: Role[]; loadErrors: APIResponse[] } } = $props();
	let roles = $state(untrack(() => data.roles));

	onMount(() => {
		for (const loadError of data.loadErrors) handleAPIError(loadError);
	});

	async function refresh() {
		const result = await listRoles();
		if (isAPIResponse(result)) {
			handleAPIError(result);
			return;
		}
		roles = result;
		await loadPermissions(true);
	}

	// Administrator holds every privilege.
	let privilegeOptions = $derived(
		(roles.find((role) => role.name === 'Administrator')?.privileges ?? []).map((privilege) => ({
			label: privilege,
			value: privilege
		}))
	);

	function emptyEditor() {
		return {
			open: false,
			editing: false,
			name: '',
			privileges: { open: false, value: [] as string[] }
		};
	}

	let editor = $state(emptyEditor());
	let deleteOpen = $state(false);
	let guideOpen = $state(false);
	let saving = $state(false);

	let tableData = $derived.by((): { rows: Row[]; columns: Column[] } => {
		const columns: Column[] = [
			{ field: 'id', title: 'ID', visible: false },
			{ field: 'name', title: 'Name' },
			{ field: 'kind', title: 'Type' },
			{ field: 'privileges', title: 'Privileges', formatter: 'textarea', widthGrow: 4 }
		];
		const rows: Row[] = roles.map((role) => ({
			id: role.name,
			name: role.name,
			kind: role.builtin ? 'Built-in' : 'Custom',
			privileges: role.privileges.join(', ') || 'None'
		}));
		return { columns, rows };
	});

	let query = $state('');
	let activeRows: Row[] | null = $state(null);
	let activeRole = $derived(roles.find((role) => role.name === activeRows?.[0]?.id) ?? null);

	async function onSave() {
		const name = editor.name.trim();
		if (!name || editor.privileges.value.length === 0) {
			toast.error('Name and privileges are required', { position: 'bottom-center' });
			return;
		}

		saving = true;
		try {
			const result = await setRole(name, editor.privileges.value);
			if (result.status !== 'success') {
				handleAPIError(result);
				toast.error('Failed to save role', { position: 'bottom-center' });
				return;
			}
			toast.success('Role saved', { position: 'bottom-center' });
			editor = emptyEditor();
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
			onclick={() => (editor = { ...emptyEditor(), open: true })}
			size="sm"
			class="h-6"
			disabled={saving}
		>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>
		{#if activeRole && !activeRole.builtin}
			<Button
				onclick={() =>
					(editor = {
						open: true,
						editing: true,
						name: activeRole.name,
						privileges: { open: false, value: [...activeRole.privileges] }
					})}
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
		<Button onclick={() => (guideOpen = true)} size="sm" variant="outline" class="ml-auto h-6">
			<div class="flex items-center">
				<span class="icon-[mdi--help-circle-outline] mr-1 h-4 w-4"></span>
				<span>Role Guide</span>
			</div>
		</Button>
	</div>

	<TreeTable
		data={tableData}
		name="tt-permission-roles"
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
		bind:query
	/>
</div>

<RoleGuide bind:open={guideOpen} {roles} />

{#if editor.open}
	<Dialog.Root bind:open={editor.open}>
		<Dialog.Content
			class="sm:max-w-120"
			onInteractOutside={(e) => e.preventDefault()}
			onEscapeKeydown={(e) => e.preventDefault()}
			showCloseButton={!saving}
			onClose={() => (editor = emptyEditor())}
		>
			<Dialog.Header>
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[mdi--card-account-details-outline]"
						size="h-5 w-5"
						gap="gap-2"
						title={editor.editing ? `Edit ${editor.name}` : 'New Role'}
					/>
				</Dialog.Title>
			</Dialog.Header>

			{#if !editor.editing}
				<CustomValueInput
					label="Name"
					placeholder="Operator"
					bind:value={editor.name}
					classes="flex-1 space-y-1.5"
					disabled={saving}
				/>
			{/if}

			<CustomComboBox
				label="Privileges"
				placeholder="Select privileges"
				bind:open={editor.privileges.open}
				bind:value={editor.privileges.value}
				data={privilegeOptions}
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
	customTitle={activeRole
		? `Delete the role ${activeRole.name}? Every grant of it is removed too.`
		: undefined}
	names={{ parent: 'role', element: activeRole?.name || '' }}
	actions={{
		onConfirm: async () => {
			if (saving || !activeRole) return;
			saving = true;
			try {
				const result = await deleteRole(activeRole.name);
				if (result.status !== 'success') {
					handleAPIError(result);
					toast.error('Failed to delete role', { position: 'bottom-center' });
					return;
				}
				toast.success('Role deleted', { position: 'bottom-center' });
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
