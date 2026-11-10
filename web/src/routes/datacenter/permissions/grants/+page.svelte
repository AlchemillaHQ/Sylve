<script lang="ts">
	import { deleteGrant, listGrants, setGrant } from '$lib/api/auth/permissions';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { loadPermissions, permissions } from '$lib/stores/permissions.svelte';
	import { subjectOf, type ACLGrant, type ACLGroup, type Role, type User } from '$lib/types/auth';
	import type { APIResponse } from '$lib/types/common';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { onMount, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Data {
		grants: ACLGrant[];
		roles: Role[];
		users: User[];
		groups: ACLGroup[];
		loadErrors: APIResponse[];
	}

	let { data }: { data: Data } = $props();
	let grants = $state(untrack(() => data.grants));

	onMount(() => {
		for (const loadError of data.loadErrors) handleAPIError(loadError);
	});

	async function refresh() {
		const result = await listGrants();
		if (isAPIResponse(result)) {
			handleAPIError(result);
			return;
		}
		grants = result;
		await loadPermissions(true);
	}

	// Node paths use IDs, which a standalone node shows nowhere else; offer them by hostname.
	let nodeNames = $derived(
		new Map(
			Object.entries(permissions.data?.nodes ?? {}).map(([hostname, id]) => [
				`/nodes/${id}`,
				hostname
			])
		)
	);
	let pathOptions = $derived([
		...['/', '/nodes'].map((path) => ({ label: path, value: path })),
		...[...nodeNames].map(([path, hostname]) => ({
			label: `Node ${hostname}`,
			value: path,
			description: path
		})),
		...['/vm', '/jail', '/container', '/storage', '/switch', '/access'].map((path) => ({
			label: path,
			value: path
		}))
	]);

	function grantForm(grant?: ACLGrant) {
		return {
			open: false,
			editing: grant ?? null,
			path: { open: false, value: grant?.path ?? '/' },
			subjectType: { open: false, value: grant?.subjectType ?? 'user' },
			subject: { open: false, value: grant?.subject ?? '' },
			role: { open: false, value: grant?.role ?? '' },
			propagate: grant?.propagate ?? true
		};
	}

	let form = $state(grantForm());
	let deleteOpen = $state(false);
	let saving = $state(false);

	let subjectOptions = $derived(
		form.subjectType.value === 'user'
			? data.users.map((user) => {
					const subject = subjectOf(user);
					return { label: subject, value: subject };
				})
			: data.groups.map((group) => ({ label: group.name, value: group.name }))
	);
	let roleOptions = $derived(data.roles.map((role) => ({ label: role.name, value: role.name })));

	function grantKey(grant: ACLGrant): string {
		return [grant.path, grant.subjectType, grant.subject, grant.role].join('\u0000');
	}

	async function onSave() {
		const grant: ACLGrant = {
			path: form.path.value.trim(),
			subjectType: form.subjectType.value as ACLGrant['subjectType'],
			subject: form.subject.value.trim(),
			role: form.role.value,
			propagate: form.propagate
		};
		if (!grant.path || !grant.subject || !grant.role) {
			toast.error('Path, user or group and role are required', { position: 'bottom-center' });
			return;
		}

		const previous = form.editing;
		const updated = previous !== null || grants.some((g) => grantKey(g) === grantKey(grant));

		saving = true;
		try {
			const result = await setGrant(grant);
			if (result.status !== 'success') {
				handleAPIError(result);
				toast.error('Failed to save grant', { position: 'bottom-center' });
				return;
			}
			// Add before removing, so editing your own grant never leaves you without it.
			const removed =
				previous && grantKey(previous) !== grantKey(grant) ? await deleteGrant(previous) : null;
			if (removed && removed.status !== 'success') {
				handleAPIError(removed);
				toast.error('Failed to remove the old grant', { position: 'bottom-center' });
			} else {
				toast.success(updated ? 'Grant updated' : 'Grant added', { position: 'bottom-center' });
			}
			form = grantForm();
			activeRows = null;
			await refresh();
		} finally {
			saving = false;
		}
	}

	let tableData = $derived.by((): { rows: Row[]; columns: Column[] } => {
		const columns: Column[] = [
			{ field: 'id', title: 'ID', visible: false },
			{ field: 'path', title: 'Path' },
			{ field: 'subjectType', title: 'Type' },
			{ field: 'subject', title: 'User or Group' },
			{ field: 'role', title: 'Role' },
			{ field: 'propagate', title: 'Propagate' }
		];
		const rows: Row[] = grants.map((grant) => ({
			id: grantKey(grant),
			path: nodeNames.has(grant.path) ? `${grant.path} (${nodeNames.get(grant.path)})` : grant.path,
			subjectType: grant.subjectType === 'user' ? 'User' : 'Group',
			subject: grant.subject,
			role: grant.role,
			propagate: grant.propagate ? 'Yes' : 'No'
		}));
		return { columns, rows };
	});

	let query = $state('');
	let activeRows: Row[] | null = $state(null);
	let activeGrant = $derived(
		grants.find((grant) => grantKey(grant) === activeRows?.[0]?.id) ?? null
	);
</script>

<div class="flex h-full flex-col overflow-hidden">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			onclick={() => (form = { ...grantForm(), open: true })}
			size="sm"
			class="h-6"
			disabled={saving}
		>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>
		{#if activeGrant}
			<Button
				onclick={() => (form = { ...grantForm(activeGrant), open: true })}
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
		name="tt-permission-grants"
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
			showResetButton={!saving}
			showCloseButton={!saving}
			onClose={() => (form = grantForm())}
			onReset={() => (form = { ...grantForm(form.editing ?? undefined), open: true })}
		>
			<Dialog.Header>
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[mdi--account-key]"
						size="h-5 w-5"
						gap="gap-2"
						title={form.editing ? 'Edit Grant' : 'New Grant'}
					/>
				</Dialog.Title>
			</Dialog.Header>

			<CustomComboBox
				label="Path"
				placeholder="Select or type a path"
				hint="/, /nodes/<node id>, /vm/<id>, /jail/<id>, /container/<id>, /storage/<pool>, /switch/<name> or /access"
				bind:open={form.path.open}
				bind:value={form.path.value}
				data={pathOptions}
				allowCustom={true}
				width="w-full"
				disabled={saving}
			/>

			<CustomComboBox
				label="Type"
				bind:open={form.subjectType.open}
				bind:value={form.subjectType.value}
				data={[
					{ label: 'User', value: 'user' },
					{ label: 'Group', value: 'group' }
				]}
				onValueChange={() => (form.subject.value = '')}
				disallowEmpty={true}
				width="w-full"
				disabled={saving}
			/>

			<CustomComboBox
				label={form.subjectType.value === 'user' ? 'User' : 'Group'}
				placeholder={form.subjectType.value === 'user' ? 'Select a user' : 'Select a group'}
				hint={form.subjectType.value === 'user'
					? 'Users of this node, or type name@realm (pam, sylve or an OpenID realm)'
					: 'Groups from Datacenter → Permissions → Groups'}
				bind:open={form.subject.open}
				bind:value={form.subject.value}
				data={subjectOptions}
				allowCustom={form.subjectType.value === 'user'}
				width="w-full"
				disabled={saving}
			/>

			<CustomComboBox
				label="Role"
				placeholder="Select a role"
				bind:open={form.role.open}
				bind:value={form.role.value}
				data={roleOptions}
				width="w-full"
				disabled={saving}
			/>

			<CustomCheckbox
				label="Propagate to everything below this path"
				bind:checked={form.propagate}
				disabled={saving}
			/>

			<Dialog.Footer class="flex justify-end">
				<Button onclick={() => onSave()} type="submit" size="sm" disabled={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						Saving...
					{:else}
						{form.editing ? 'Save' : 'Add'}
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
	names={{
		parent: 'grant',
		element: activeGrant
			? `${activeGrant.role} for ${activeGrant.subject} on ${activeGrant.path}`
			: ''
	}}
	actions={{
		onConfirm: async () => {
			if (saving || !activeGrant) return;
			saving = true;
			try {
				const result = await deleteGrant(activeGrant);
				if (result.status !== 'success') {
					handleAPIError(result);
					toast.error('Failed to delete grant', { position: 'bottom-center' });
					return;
				}
				toast.success('Grant deleted', { position: 'bottom-center' });
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
