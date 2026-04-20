<script lang="ts">
	import {
		createNotificationRule,
		deleteNotificationRule,
		getNotificationRules,
		updateNotificationRule
	} from '$lib/api/notifications';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { NotificationRulesConfig } from '$lib/types/notifications';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { renderWithIcon } from '$lib/utils/table';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';
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
			title: 'Template'
		},
		{
			field: 'targetLabel',
			title: 'Target / Rules'
		},
		{
			field: 'active',
			title: 'Status',
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
			title: 'In-App',
			formatter: (cell: CellComponent) => {
				const rowData = cell.getRow().getData() as RuleRow;
				if (rowData.isTemplateRow) {
					return '-';
				}
				return cell.getValue()
					? renderWithIcon('mdi:check', 'On', 'text-green-500')
					: renderWithIcon('mdi:close', 'Off', 'text-muted-foreground');
			}
		},
		{
			field: 'ntfyEnabled',
			title: 'ntfy',
			formatter: (cell: CellComponent) => {
				const rowData = cell.getRow().getData() as RuleRow;
				if (rowData.isTemplateRow) {
					return '-';
				}
				return cell.getValue()
					? renderWithIcon('mdi:check', 'On', 'text-green-500')
					: renderWithIcon('mdi:close', 'Off', 'text-muted-foreground');
			}
		},
		{
			field: 'emailEnabled',
			title: 'Email',
			formatter: (cell: CellComponent) => {
				const rowData = cell.getRow().getData() as RuleRow;
				if (rowData.isTemplateRow) {
					return '-';
				}
				return cell.getValue()
					? renderWithIcon('mdi:check', 'On', 'text-green-500')
					: renderWithIcon('mdi:close', 'Off', 'text-muted-foreground');
			}
		}
	]);

	let tableData = $derived.by(() => {
		const templateRows = new Map<string, RuleRow>();

		for (const template of currentConfig.templates || []) {
			templateRows.set(template.key, {
				id: `template-${template.key}`,
				templateKey: template.key,
				templateLabel: template.label,
				targetLabel: template.targets.length === 1 ? '1 target' : `${template.targets.length} targets`,
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
	let activeRows: Row[] | null = $state(null);
	let activeRow: RuleRow | null = $derived(activeRows && activeRows.length === 1 ? (activeRows[0] as RuleRow) : null);
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

	let createTemplateOptions = $derived.by(() => {
		return (currentConfig.templates || []).map((template) => ({
			key: template.key,
			label: template.label
		}));
	});

	let existingRuleTargets = $derived.by(() => {
		const set = new Set<string>();
		for (const rule of currentConfig.rules || []) {
			set.add(`${rule.templateKey}::${rule.targetKey}`);
		}
		return set;
	});

	let createTargetOptions = $derived.by(() => {
		const template = (currentConfig.templates || []).find((item) => item.key === modals.create.templateKey);
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
			toast.error('Failed to delete notification rule', { position: 'bottom-center' });
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
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>New</span>
			</div>
		</Button>
		{#if selectedRule}
			<Button size="sm" variant="outline" class="h-6.5" onclick={openEditModal}>
				<div class="flex items-center">
					<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>
					<span>Edit</span>
				</div>
			</Button>
			<Button size="sm" variant="outline" class="h-6.5" onclick={openDeleteModal}>
				<div class="flex items-center">
					<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
					<span>Delete</span>
				</div>
			</Button>
		{/if}
		<Button size="sm" variant="outline" class="ml-auto h-6.5" onclick={refreshRules} disabled={busy.refresh}>
			{#if busy.refresh}
				<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>
			{/if}
			<span>Refresh</span>
		</Button>
	</div>

	<TreeTable
		data={tableData}
		name="tt-notification-rules"
		bind:parentActiveRow={activeRows}
		bind:query
		multipleSelect={false}
		rowFormatter={rowFormatter}
		customPlaceholder="No notification rules available"
	/>
</div>

{#if modals.create.open}
	<Dialog.Root bind:open={modals.create.open}>
		<Dialog.Content class="sm:max-w-106.25" onInteractOutside={(e) => e.preventDefault()}>
			<Dialog.Header>
				<Dialog.Title class="flex items-center gap-2">
					<span class="icon-[gg--add] h-4 w-4"></span>
					<span>New Notification Rule</span>
				</Dialog.Title>
			</Dialog.Header>

			<div class="space-y-3">
				<div class="space-y-1">
					<p class="text-sm font-medium">Template</p>
					<select
						class="border-input bg-background ring-offset-background placeholder:text-muted-foreground focus-visible:ring-ring flex h-9 w-full rounded-md border px-3 py-1 text-sm focus-visible:ring-1 focus-visible:outline-none"
						bind:value={modals.create.templateKey}
					>
						{#each createTemplateOptions as template (template.key)}
							<option value={template.key}>{template.label}</option>
						{/each}
					</select>
				</div>

				<div class="space-y-1">
					<p class="text-sm font-medium">Target</p>
					<select
						class="border-input bg-background ring-offset-background placeholder:text-muted-foreground focus-visible:ring-ring flex h-9 w-full rounded-md border px-3 py-1 text-sm focus-visible:ring-1 focus-visible:outline-none"
						bind:value={modals.create.targetKey}
						disabled={createTargetOptions.length === 0}
					>
						{#if createTargetOptions.length === 0}
							<option value="">No available targets</option>
						{:else}
							{#each createTargetOptions as target (target.key)}
								<option value={target.key}>{target.label}</option>
							{/each}
						{/if}
					</select>
					{#if createTargetOptions.length === 0}
						<p class="text-muted-foreground text-xs">All available targets already have rules.</p>
					{/if}
				</div>

				<div class="space-y-2 pt-1">
					<p class="text-sm font-medium">Channels</p>
					<label class="flex items-center gap-2 text-sm">
						<input type="checkbox" bind:checked={modals.create.uiEnabled} class="h-4 w-4" />
						<span>In-App</span>
					</label>
					<label class="flex items-center gap-2 text-sm">
						<input type="checkbox" bind:checked={modals.create.ntfyEnabled} class="h-4 w-4" />
						<span>ntfy</span>
					</label>
					<label class="flex items-center gap-2 text-sm">
						<input type="checkbox" bind:checked={modals.create.emailEnabled} class="h-4 w-4" />
						<span>Email</span>
					</label>
				</div>
			</div>

			<Dialog.Footer>
				<Button onclick={createRule} size="sm" disabled={busy.create || createTargetOptions.length === 0}>
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
		<Dialog.Content class="sm:max-w-106.25" onInteractOutside={(e) => e.preventDefault()}>
			<Dialog.Header>
				<Dialog.Title class="flex items-center gap-2">
					<span class="icon-[mdi--pencil] h-4 w-4"></span>
					<span>Edit Notification Rule</span>
				</Dialog.Title>
			</Dialog.Header>

			<div class="space-y-2 text-sm">
				<p><span class="font-medium">Template:</span> {modals.edit.templateLabel}</p>
				<p><span class="font-medium">Target:</span> {modals.edit.targetLabel}</p>
				<p>
					<span class="font-medium">Status:</span>
					{modals.edit.active ? 'Active' : 'Inactive (target is not currently active)'}
				</p>
			</div>

			<div class="space-y-2 pt-1">
				<p class="text-sm font-medium">Channels</p>
				<label class="flex items-center gap-2 text-sm">
					<input type="checkbox" bind:checked={modals.edit.uiEnabled} class="h-4 w-4" />
					<span>In-App</span>
				</label>
				<label class="flex items-center gap-2 text-sm">
					<input type="checkbox" bind:checked={modals.edit.ntfyEnabled} class="h-4 w-4" />
					<span>ntfy</span>
				</label>
				<label class="flex items-center gap-2 text-sm">
					<input type="checkbox" bind:checked={modals.edit.emailEnabled} class="h-4 w-4" />
					<span>Email</span>
				</label>
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
