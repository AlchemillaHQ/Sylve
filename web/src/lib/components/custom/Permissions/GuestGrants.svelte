<script lang="ts">
	import { listUsers } from '$lib/api/auth/local';
	import {
		deleteGrant,
		listACLGroups,
		listGrants,
		listRoles,
		setGrant
	} from '$lib/api/auth/permissions';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { allowed, loadPermissions } from '$lib/stores/permissions.svelte';
	import { subjectOf, type ACLGrant, type ACLGroup, type Role, type User } from '$lib/types/auth';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';

	let { path }: { path: string } = $props();

	const grants = resource(
		() => path,
		async () => {
			const result = await listGrants();
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return [] as ACLGrant[];
			}
			return result.filter((grant) => grant.path === path);
		},
		{ initialValue: [] as ACLGrant[] }
	);
	const choices = resource(
		() => path,
		async () => {
			const [roles, users, groups] = await Promise.all([listRoles(), listUsers(), listACLGroups()]);
			return {
				roles: isAPIResponse(roles) ? ([] as Role[]) : roles,
				users: isAPIResponse(users) ? ([] as User[]) : users,
				groups: isAPIResponse(groups) ? ([] as ACLGroup[]) : groups
			};
		}
	);

	let canModify = $derived(allowed('Permissions.Modify', path));

	function grantForm() {
		return {
			open: false,
			subjectType: { open: false, value: 'user' },
			subject: { open: false, value: '' },
			role: { open: false, value: '' }
		};
	}

	let form = $state(grantForm());
	let deleteOpen = $state(false);
	let saving = $state(false);

	let subjectOptions = $derived(
		form.subjectType.value === 'user'
			? (choices.current?.users ?? []).map((user) => {
					const subject = subjectOf(user);
					return { label: subject, value: subject };
				})
			: (choices.current?.groups ?? []).map((group) => ({ label: group.name, value: group.name }))
	);
	let roleOptions = $derived(
		(choices.current?.roles ?? []).map((role) => ({ label: role.name, value: role.name }))
	);

	function grantKey(grant: ACLGrant): string {
		return [grant.subjectType, grant.subject, grant.role].join('\u0000');
	}

	let tableData = $derived.by((): { rows: Row[]; columns: Column[] } => ({
		columns: [
			{ field: 'id', title: 'ID', visible: false },
			{ field: 'subjectType', title: 'Type' },
			{ field: 'subject', title: 'User or Group' },
			{ field: 'role', title: 'Role' }
		],
		rows: grants.current.map((grant) => ({
			id: grantKey(grant),
			subjectType: grant.subjectType === 'user' ? 'User' : 'Group',
			subject: grant.subject,
			role: grant.role
		}))
	}));

	let activeRows: Row[] | null = $state(null);
	let activeGrant = $derived(
		grants.current.find((grant) => grantKey(grant) === activeRows?.[0]?.id) ?? null
	);

	async function refresh() {
		await grants.refetch();
		await loadPermissions(true);
	}

	async function onAdd() {
		if (!form.subject.value.trim() || !form.role.value) {
			toast.error('User or group and role are required', { position: 'bottom-center' });
			return;
		}
		saving = true;
		try {
			const result = await setGrant({
				path,
				subjectType: form.subjectType.value as ACLGrant['subjectType'],
				subject: form.subject.value.trim(),
				role: form.role.value,
				propagate: true
			});
			if (result.status !== 'success') {
				handleAPIError(result);
				toast.error('Failed to add grant', { position: 'bottom-center' });
				return;
			}
			toast.success('Grant added', { position: 'bottom-center' });
			form = grantForm();
			await refresh();
		} finally {
			saving = false;
		}
	}
</script>

<div class="flex h-full flex-col overflow-hidden">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Button
			onclick={() => (form = { ...grantForm(), open: true })}
			size="sm"
			class="h-6"
			disabled={saving || !canModify}
		>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="Add" />
		</Button>
		{#if activeGrant}
			<Button
				onclick={() => (deleteOpen = true)}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={saving || !canModify}
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Remove" />
			</Button>
		{/if}
	</div>

	<TreeTable
		data={tableData}
		name="tt-guest-grants"
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
	/>
</div>

{#if form.open}
	<Dialog.Root bind:open={form.open}>
		<Dialog.Content
			class="sm:max-w-120"
			onInteractOutside={(e) => e.preventDefault()}
			onEscapeKeydown={(e) => e.preventDefault()}
			showCloseButton={!saving}
			onClose={() => (form = grantForm())}
		>
			<Dialog.Header>
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[mdi--account-key]"
						size="h-5 w-5"
						gap="gap-2"
						title={`Add a grant on ${path}`}
					/>
				</Dialog.Title>
			</Dialog.Header>

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

			<Dialog.Footer class="flex justify-end">
				<Button onclick={() => onAdd()} type="submit" size="sm" disabled={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						Adding...
					{:else}
						Add
					{/if}
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}

<AlertDialog
	open={deleteOpen}
	loading={saving}
	loadingLabel="Removing..."
	keepOpenOnConfirm={true}
	names={{
		parent: 'grant',
		element: activeGrant ? `${activeGrant.role} for ${activeGrant.subject}` : ''
	}}
	actions={{
		onConfirm: async () => {
			if (saving || !activeGrant) return;
			saving = true;
			try {
				const result = await deleteGrant(activeGrant);
				if (result.status !== 'success') {
					handleAPIError(result);
					toast.error('Failed to remove grant', { position: 'bottom-center' });
					return;
				}
				toast.success('Grant removed', { position: 'bottom-center' });
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
