<script lang="ts">
	import {
		createNotificationRule,
		deleteNotificationRule,
		getNotificationRules,
		updateNotificationRule
	} from '$lib/api/notifications';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { NotificationRulesConfig } from '$lib/types/notifications';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { renderWithIcon } from '$lib/utils/table';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';
	import { SvelteMap, SvelteSet } from 'svelte/reactivity';
	import type { CellComponent, RowComponent } from 'tabulator-tables';

	interface Data {
		rules: NotificationRulesConfig;
	}

	interface RuleRow extends Row {
		ruleId?: number;
		templateKey: string;
		templateLabel: string;
		targetKey?: string;
		targetLabel: string;
		active: boolean;
		uiEnabled: boolean;
		ntfyEnabled: boolean;
		emailEnabled: boolean;
		isTemplateRow: boolean;
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	let rulesResource = resource(
		() => 'notification-rules',
		async (key) => {
			const loaded = await getNotificationRules();
			if (!isAPIResponse(loaded)) {
				updateCache(key, loaded);
			}
			return isAPIResponse(loaded) ? data.rules : loaded;
		},
		{ initialValue: data.rules }
	);

	let currentConfig = $derived(rulesResource.current as NotificationRulesConfig);

	let columns: Column[] = $state([
		{ field: 'id', title: 'ID', visible: false },
		{
			field: 'templateLabel',
			title: 'Name',
			formatter: (cell: CellComponent) => {
				const rowData = cell.getRow().getData() as RuleRow;
				return rowData.isTemplateRow ? cell.getValue() || '' : rowData.targetLabel;
			}
		},
		{
			field: 'targetLabel',
			title: 'Rules',
			visible: false
		},
		{
			field: 'active',
			title: 'Status',
			width: 110,
			formatter: (cell: CellComponent) => {
				const rowData = cell.getRow().getData() as RuleRow;
				if (rowData.isTemplateRow) {
					return '-';
				}
				return rowData.active
					? renderWithIcon('mdi:check-circle', 'Active', 'text-green-500')
					: renderWithIcon('mdi:close-circle', 'Inactive', 'text-muted-foreground');
			}
		},
		{
			field: 'uiEnabled',
			title: 'Channels',
			width: 200,
			formatter: (cell: CellComponent) => {
				const rowData = cell.getRow().getData() as RuleRow;
				if (rowData.isTemplateRow) {
					return '-';
				}
				const channels: { field: keyof RuleRow; label: string; icon: string }[] = [
					{ field: 'uiEnabled', label: 'In-App', icon: 'icon-[mdi--monitor]' },
					{ field: 'ntfyEnabled', label: 'ntfy', icon: 'icon-[mdi--bell]' },
					{ field: 'emailEnabled', label: 'Email', icon: 'icon-[mdi--email-outline]' }
				];
				const parts = channels.map(({ field, label, icon }) => {
					const enabled = rowData[field];
					const cls = enabled ? 'text-green-500' : 'text-muted-foreground opacity-40';
					return `<span class="inline-flex items-center gap-0.5 ${cls}"><span class="${icon} h-3.5 w-3.5 shrink-0"></span><span>${label}</span></span>`;
				});
				return `<span class="inline-flex items-center gap-3">${parts.join('')}</span>`;
			}
		}
	]);

	let tableData = $derived.by(() => {
		const templateRows = new SvelteMap<string, RuleRow>();

		for (const template of currentConfig.templates || []) {
			templateRows.set(template.key, {
				id: `template-${template.key}`,
				templateKey: template.key,
				templateLabel: template.label,
				targetLabel:
					template.targets.length === 1 ? '1 target' : `${template.targets.length} targets`,
				active: true,
				uiEnabled: false,
				ntfyEnabled: false,
				emailEnabled: false,
				isTemplateRow: true,
				children: []
			});
		}

		for (const rule of currentConfig.rules || []) {
			const child: RuleRow = {
				id: rule.id,
				ruleId: rule.id,
				templateKey: rule.templateKey,
				templateLabel: rule.templateLabel,
				targetKey: rule.targetKey,
				targetLabel: rule.targetLabel,
				active: rule.active,
				uiEnabled: rule.uiEnabled,
				ntfyEnabled: rule.ntfyEnabled,
				emailEnabled: rule.emailEnabled,
				isTemplateRow: false
			};

			if (!templateRows.has(rule.templateKey)) {
				templateRows.set(rule.templateKey, {
					id: `template-${rule.templateKey}`,
					templateKey: rule.templateKey,
					templateLabel: rule.templateLabel,
					targetLabel: '0 targets',
					active: true,
					uiEnabled: false,
					ntfyEnabled: false,
					emailEnabled: false,
					isTemplateRow: true,
					children: []
				});
			}

			const parent = templateRows.get(rule.templateKey) as RuleRow;
			(parent.children as RuleRow[]).push(child);
		}

		let rows = Array.from(templateRows.values())
			.map((parent) => {
				const children = (parent.children as RuleRow[]).sort((a, b) =>
					a.targetLabel.localeCompare(b.targetLabel)
				);
				const count = children.length;
				return {
					...parent,
					targetLabel: count === 1 ? '1 rule' : `${count} rules`,
					children
				};
			})
			.sort((a, b) => a.templateLabel.localeCompare(b.templateLabel));

		return {
			columns,
			rows
		};
	});

	let query: string = $state('');
	let activeRows = $state<Row[] | null>(null);
	let activeRow: RuleRow | null = $derived(
		activeRows && Array.isArray(activeRows) && activeRows.length === 1
			? (activeRows[0] as RuleRow)
			: null
	);
	let selectedRule = $derived(
		activeRow && !activeRow.isTemplateRow && activeRow.ruleId ? (activeRow as RuleRow) : null
	);

	let busy = $state({
		refresh: false,
		create: false,
		edit: false,
		delete: false
	});

	let modals = $state({
		create: {
			open: false,
			templateKey: '',
			targetKey: '',
			uiEnabled: true,
			ntfyEnabled: true,
			emailEnabled: true
		},
		edit: {
			open: false,
			id: 0,
			templateLabel: '',
			targetLabel: '',
			active: true,
			uiEnabled: true,
			ntfyEnabled: true,
			emailEnabled: true
		},
		delete: {
			open: false,
			id: 0,
			name: ''
		}
	});

	let editOriginal = $state({ uiEnabled: true, ntfyEnabled: true, emailEnabled: true });

	let createTemplateOptions = $derived.by(() => {
		return (currentConfig.templates || []).map((template) => ({
			key: template.key,
			label: template.label
		}));
	});

	let existingRuleTargets = $derived.by(() => {
		const set = new SvelteSet<string>();
		for (const rule of currentConfig.rules || []) {
			set.add(`${rule.templateKey}::${rule.targetKey}`);
		}
		return set;
	});

	let createTargetOptions = $derived.by(() => {
		const template = (currentConfig.templates || []).find(
			(item) => item.key === modals.create.templateKey
		);
		if (!template) {
			return [];
		}
		return (template.targets || []).filter(
			(target) => !existingRuleTargets.has(`${template.key}::${target.key}`)
		);
	});

	$effect(() => {
		if (!modals.create.open) {
			return;
		}
		if (createTargetOptions.length === 0) {
			modals.create.targetKey = '';
			return;
		}
		if (!createTargetOptions.some((target) => target.key === modals.create.targetKey)) {
			modals.create.targetKey = createTargetOptions[0].key;
		}
	});

	function rowFormatter(row: RowComponent) {
		const rowData = row.getData() as RuleRow;
		const element = row.getElement();
		element.classList.remove('opacity-60');
		if (!rowData.isTemplateRow && !rowData.active) {
			element.classList.add('opacity-60');
		}
	}

	async function refreshRules() {
		if (busy.refresh) {
			return;
		}

		busy.refresh = true;
		await rulesResource.refetch();
		busy.refresh = false;
	}

	function openCreateModal() {
		const firstTemplate = createTemplateOptions[0]?.key || '';
		modals.create = {
			open: true,
			templateKey: firstTemplate,
			targetKey: '',
			uiEnabled: true,
			ntfyEnabled: true,
			emailEnabled: true
		};
	}

	function openEditModal() {
		if (!selectedRule) {
			return;
		}

		modals.edit = {
			open: true,
			id: selectedRule.ruleId as number,
			templateLabel: selectedRule.templateLabel,
			targetLabel: selectedRule.targetLabel,
			active: selectedRule.active,
			uiEnabled: selectedRule.uiEnabled,
			ntfyEnabled: selectedRule.ntfyEnabled,
			emailEnabled: selectedRule.emailEnabled
		};

		editOriginal = {
			uiEnabled: selectedRule.uiEnabled,
			ntfyEnabled: selectedRule.ntfyEnabled,
			emailEnabled: selectedRule.emailEnabled
		};
	}

	function openDeleteModal() {
		if (!selectedRule) {
			return;
		}

		modals.delete = {
			open: true,
			id: selectedRule.ruleId as number,
			name: `${selectedRule.templateLabel} / ${selectedRule.targetLabel}`
		};
	}

	async function createRule() {
		if (busy.create) {
			return;
		}
		if (!modals.create.templateKey || !modals.create.targetKey) {
			toast.error('Choose a template and target before creating a rule', {
				position: 'bottom-center'
			});
			return;
		}

		busy.create = true;
		const response = await createNotificationRule({
			templateKey: modals.create.templateKey,
			targetKey: modals.create.targetKey,
			uiEnabled: modals.create.uiEnabled,
			ntfyEnabled: modals.create.ntfyEnabled,
			emailEnabled: modals.create.emailEnabled
		});
		busy.create = false;

		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to create notification rule', { position: 'bottom-center' });
			return;
		}

		toast.success('Notification rule created', { position: 'bottom-center' });
		modals.create.open = false;
		activeRows = null;
		await rulesResource.refetch();
	}

	async function saveRuleChanges() {
		if (busy.edit || modals.edit.id === 0) {
			return;
		}

		busy.edit = true;
		const response = await updateNotificationRule(modals.edit.id, {
			uiEnabled: modals.edit.uiEnabled,
			ntfyEnabled: modals.edit.ntfyEnabled,
			emailEnabled: modals.edit.emailEnabled
		});
		busy.edit = false;

		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to update notification rule', { position: 'bottom-center' });
			return;
		}

		toast.success('Notification rule updated', { position: 'bottom-center' });
		modals.edit.open = false;
		activeRows = null;
		await rulesResource.refetch();
	}

	async function deleteRule() {
		if (busy.delete || modals.delete.id === 0) {
			return;
		}

		busy.delete = true;
		const response = await deleteNotificationRule(modals.delete.id);
		busy.delete = false;

		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			if (response.error === 'notification_rule_auto_managed_active') {
				toast.error('Cannot delete an auto-managed rule while its target is active', {
					position: 'bottom-center'
				});
			} else {
				toast.error('Failed to delete notification rule', { position: 'bottom-center' });
			}
			modals.delete.open = false;
			return;
		}

		toast.success('Notification rule deleted', { position: 'bottom-center' });
		modals.delete.open = false;
		activeRows = null;
		await rulesResource.refetch();
	}
</script>

<div class="flex h-full w-full flex-col overflow-hidden">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button size="sm" class="h-6" onclick={openCreateModal}>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>
		{#if selectedRule}
			<Button size="sm" variant="outline" class="h-6.5" onclick={openEditModal}>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
			</Button>
			<Button size="sm" variant="outline" class="h-6.5" onclick={openDeleteModal}>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
		<Button
			size="sm"
			variant="outline"
			class="ml-auto h-6.5"
			onclick={refreshRules}
			disabled={busy.refresh}
		>
			<SpanWithIcon
				icon={busy.refresh ? 'icon-[mdi--loading] animate-spin' : 'icon-[mdi--refresh]'}
				size="h-4 w-4"
				gap="gap-2"
				title="Refresh"
			/>
		</Button>
	</div>

	<TreeTable
		data={tableData}
		name="tt-notification-rules"
		bind:parentActiveRow={activeRows}
		bind:query
		multipleSelect={false}
		{rowFormatter}
		customPlaceholder="No notification rules available"
	/>
</div>

{#if modals.create.open}
	<Dialog.Root bind:open={modals.create.open}>
		<Dialog.Content
			class="sm:max-w-106.25"
			onInteractOutside={(e) => e.preventDefault()}
			showCloseButton={true}
			onClose={() => (modals.create.open = false)}
		>
			<Dialog.Header>
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[gg--add]"
						size="h-5 w-5"
						gap="gap-2"
						title="New Notification Rule"
					/>
				</Dialog.Title>
			</Dialog.Header>

			<div class="space-y-3">
				<SimpleSelect
					label="Template"
					options={createTemplateOptions.map((t) => ({ value: t.key, label: t.label }))}
					bind:value={modals.create.templateKey}
					onChange={(v) => (modals.create.templateKey = v)}
					classes={{
						parent: 'space-y-1',
						label: 'text-sm font-medium',
						trigger: 'inline-flex h-9 w-full items-center overflow-hidden px-3 text-left'
					}}
				/>

				<div class="space-y-1">
					<SimpleSelect
						label="Target"
						options={createTargetOptions.length === 0
							? [{ value: '', label: 'No available targets' }]
							: createTargetOptions.map((t) => ({ value: t.key, label: t.label }))}
						bind:value={modals.create.targetKey}
						onChange={(v) => (modals.create.targetKey = v)}
						disabled={createTargetOptions.length === 0}
						classes={{
							parent: 'space-y-1',
							label: 'text-sm font-medium',
							trigger: 'inline-flex h-9 w-full items-center overflow-hidden px-3 text-left'
						}}
						title={createTargetOptions.length === 0
							? 'All available targets already have rules'
							: ''}
					/>
				</div>

				<div class="space-y-2 pt-1">
					<p class="text-sm font-medium">Channels</p>
					<div class="flex items-center gap-4">
						<CustomCheckbox label="In-App" bind:checked={modals.create.uiEnabled} />
						<CustomCheckbox label="ntfy" bind:checked={modals.create.ntfyEnabled} />
						<CustomCheckbox label="Email" bind:checked={modals.create.emailEnabled} />
					</div>
				</div>
			</div>

			<Dialog.Footer>
				<Button
					onclick={createRule}
					size="sm"
					disabled={busy.create || createTargetOptions.length === 0}
				>
					{#if busy.create}
						<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>
					{/if}
					Create
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}

{#if modals.edit.open}
	<Dialog.Root bind:open={modals.edit.open}>
		<Dialog.Content
			class="sm:max-w-106.25"
			onInteractOutside={(e) => e.preventDefault()}
			showCloseButton={true}
			showResetButton={true}
			onClose={() => (modals.edit.open = false)}
			onReset={() => {
				modals.edit.uiEnabled = editOriginal.uiEnabled;
				modals.edit.ntfyEnabled = editOriginal.ntfyEnabled;
				modals.edit.emailEnabled = editOriginal.emailEnabled;
			}}
		>
			<Dialog.Header>
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[mdi--pencil]"
						size="h-5 w-5"
						gap="gap-2"
						title="Edit Notification Rule"
					/>
				</Dialog.Title>
			</Dialog.Header>

			<div class="rounded-md border">
				<Table.Root>
					<Table.Header class="bg-muted/50">
						<Table.Row>
							<Table.Head>Template</Table.Head>
							<Table.Head>Target</Table.Head>
							<Table.Head>Status</Table.Head>
						</Table.Row>
					</Table.Header>
					<Table.Body>
						<Table.Row>
							<Table.Cell>{modals.edit.templateLabel}</Table.Cell>
							<Table.Cell>{modals.edit.targetLabel}</Table.Cell>
							<Table.Cell>
								{#if modals.edit.active}
									<span class="text-green-500">Active</span>
								{:else}
									<span class="text-muted-foreground">Inactive</span>
								{/if}
							</Table.Cell>
						</Table.Row>
					</Table.Body>
				</Table.Root>
			</div>

			<div class="space-y-2 pt-1">
				<p class="text-sm font-medium">Channels</p>
				<div class="flex items-center gap-4">
					<CustomCheckbox label="In-App" bind:checked={modals.edit.uiEnabled} />
					<CustomCheckbox label="ntfy" bind:checked={modals.edit.ntfyEnabled} />
					<CustomCheckbox label="Email" bind:checked={modals.edit.emailEnabled} />
				</div>
			</div>

			<Dialog.Footer>
				<Button onclick={saveRuleChanges} size="sm" disabled={busy.edit}>
					{#if busy.edit}
						<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>
					{/if}
					Save
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}

<AlertDialog
	open={modals.delete.open}
	names={{ parent: 'notification rule', element: modals.delete.name }}
	actions={{
		onConfirm: async () => {
			await deleteRule();
		},
		onCancel: () => {
			modals.delete.open = false;
		}
	}}
/>
