<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
import {
	bulkDeleteNotificationRules,
	bulkUpdateNotificationRules,
	createNotificationRule,
	deleteNotificationRule,
	getNotificationRules,
	testNotificationRule,
	updateNotificationRule
} from '$lib/api/notifications';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
import * as Accordion from '$lib/components/ui/accordion/index.js';
import * as Dialog from '$lib/components/ui/dialog/index.js';
import * as Table from '$lib/components/ui/table/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { BulkUpdateRulesInput, NotificationRulesConfig } from '$lib/types/notifications';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { renderWithIcon } from '$lib/utils/table';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';
	import { SvelteMap, SvelteSet } from 'svelte/reactivity';
	import type { CellComponent } from 'tabulator-tables';

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
		discordEnabled: boolean;
		config?: string;
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
					{ field: 'emailEnabled', label: 'Email', icon: 'icon-[mdi--email-outline]' },
					{ field: 'discordEnabled', label: 'Discord', icon: 'icon-[mdi--discord]' }
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
				discordEnabled: false,
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
				discordEnabled: rule.discordEnabled,
				config: rule.config,
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
					discordEnabled: false,
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
		delete: false,
		test: false
	});

	let modals = $state({
		create: {
			open: false,
			templateKey: '',
			targetKey: '',
			uiEnabled: true,
			ntfyEnabled: true,
			emailEnabled: true,
			discordEnabled: true
		},
		edit: {
			open: false,
			id: 0,
			templateKey: '',
			templateLabel: '',
			targetLabel: '',
			active: true,
			uiEnabled: true,
			ntfyEnabled: true,
			emailEnabled: true,
			discordEnabled: true,
			config: ''
		},
		delete: {
			open: false,
			id: 0,
			name: ''
		},
		bulkDelete: {
			open: false,
			ids: [] as number[]
		},
		bulkUpdate: {
			open: false,
			ids: [] as number[],
			rules: [] as Array<{
				id: number;
				templateKey: string;
				templateLabel: string;
				targetLabel: string;
				uiEnabled: boolean;
				ntfyEnabled: boolean;
				emailEnabled: boolean;
				discordEnabled: boolean;
			}>,
			uiEnabled: null as boolean | null,
			ntfyEnabled: null as boolean | null,
			emailEnabled: null as boolean | null,
			discordEnabled: null as boolean | null
		},
		test: {
			open: false,
			templateKey: '',
			targetKey: '',
			condition: ''
		}
	});

	let bulkUpdateGroups = $derived.by(() => {
		const map = new Map<string, { label: string; count: number }>();
		for (const r of modals.bulkUpdate.rules) {
			const existing = map.get(r.templateKey);
			if (existing) {
				existing.count++;
			} else {
				map.set(r.templateKey, { label: r.templateLabel, count: 1 });
			}
		}
		return Array.from(map.values());
	});

	let editOriginal = $state({ uiEnabled: true, ntfyEnabled: true, emailEnabled: true, discordEnabled: true, config: '' });

	function configNumber(key: string, fallback: number): number {
		try {
			const v = JSON.parse(modals.edit.config || '{}')[key];
			return typeof v === 'number' ? v : fallback;
		} catch {
			return fallback;
		}
	}

	function setConfigNumber(key: string, value: number) {
		let cfg: Record<string, number> = {};
		try {
			cfg = JSON.parse(modals.edit.config || '{}');
		} catch {
			/* empty */
		}
		cfg[key] = value;
		modals.edit.config = JSON.stringify(cfg);
	}

	let editTemplateHasConfig = $derived(
		modals.edit.templateKey === 'system.disk.smart.temperature' ||
			modals.edit.templateKey === 'system.disk.smart.wearout'
	);

	let editConfigDefaults = $derived.by(() => {
		if (!editTemplateHasConfig) return {};
		const template = (currentConfig.templates || []).find(
			(t) => t.key === modals.edit.templateKey
		);
		try {
			return JSON.parse(template?.defaultConfig || '{}');
		} catch {
			return {};
		}
	});

	let editConfigInvalid = $derived.by(() => {
		if (!editTemplateHasConfig) return false;
		if (modals.edit.templateKey === 'system.disk.smart.temperature') {
			return configNumber('warningCelsius', 55) >= configNumber('criticalCelsius', 65);
		}
		if (modals.edit.templateKey === 'system.disk.smart.wearout') {
			return configNumber('warningPercent', 80) >= configNumber('criticalPercent', 90);
		}
		return false;
	});

	let editConfigInvalidMessage = 'Warning threshold must be lower than critical';

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
			emailEnabled: true,
			discordEnabled: true
		};
	}

	function openEditModal() {
		if (!selectedRule) {
			return;
		}

		const template = (currentConfig.templates || []).find(
			(t) => t.key === selectedRule.templateKey
		);
		const config = selectedRule.config || template?.defaultConfig || '';

		modals.edit = {
			open: true,
			id: selectedRule.ruleId as number,
			templateKey: selectedRule.templateKey,
			templateLabel: selectedRule.templateLabel,
			targetLabel: selectedRule.targetLabel,
			active: selectedRule.active,
			uiEnabled: selectedRule.uiEnabled,
			ntfyEnabled: selectedRule.ntfyEnabled,
			emailEnabled: selectedRule.emailEnabled,
			discordEnabled: selectedRule.discordEnabled,
			config
		};

		editOriginal = {
			uiEnabled: selectedRule.uiEnabled,
			ntfyEnabled: selectedRule.ntfyEnabled,
			emailEnabled: selectedRule.emailEnabled,
			discordEnabled: selectedRule.discordEnabled,
			config
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

	function openBulkDeleteModal() {
		const eligibleRows = (activeRows || []).filter(
			(r) => !(r as RuleRow).isTemplateRow && (r as RuleRow).ruleId
		);
		if (eligibleRows.length < 2) {
			return;
		}

		modals.bulkDelete = {
			open: true,
			ids: eligibleRows.map((r) => (r as RuleRow).ruleId as number)
		};
	}

	function openBulkUpdateModal() {
		const eligibleRows = (activeRows || []).filter(
			(r): r is RuleRow => !(r as RuleRow).isTemplateRow && !!(r as RuleRow).ruleId
		);
		if (eligibleRows.length < 2) {
			return;
		}

		modals.bulkUpdate = {
			open: true,
			ids: eligibleRows.map((r) => r.ruleId as number),
			rules: eligibleRows.map((r) => ({
				id: r.ruleId as number,
				templateKey: r.templateKey,
				templateLabel: r.templateLabel,
				targetLabel: r.targetLabel,
				uiEnabled: r.uiEnabled,
				ntfyEnabled: r.ntfyEnabled,
				emailEnabled: r.emailEnabled,
				discordEnabled: r.discordEnabled
			})),
			uiEnabled: null,
			ntfyEnabled: null,
			emailEnabled: null,
			discordEnabled: null
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
			emailEnabled: modals.create.emailEnabled,
			discordEnabled: modals.create.discordEnabled
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
			emailEnabled: modals.edit.emailEnabled,
			discordEnabled: modals.edit.discordEnabled,
			config: modals.edit.config
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

	async function bulkDeleteRules() {
		if (busy.delete || modals.bulkDelete.ids.length === 0) {
			return;
		}

		busy.delete = true;
		const response = await bulkDeleteNotificationRules(modals.bulkDelete.ids);
		busy.delete = false;

		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to delete notification rules', { position: 'bottom-center' });
			modals.bulkDelete.open = false;
			return;
		}

		toast.success(`${modals.bulkDelete.ids.length} notification rules deleted`, { position: 'bottom-center' });
		modals.bulkDelete.open = false;
		activeRows = null;
		await rulesResource.refetch();
	}

	async function bulkUpdateRules() {
		if (busy.delete || modals.bulkUpdate.ids.length === 0) {
			return;
		}

		const payload: BulkUpdateRulesInput = { ids: modals.bulkUpdate.ids };
		if (modals.bulkUpdate.uiEnabled !== null) payload.uiEnabled = modals.bulkUpdate.uiEnabled;
		if (modals.bulkUpdate.ntfyEnabled !== null) payload.ntfyEnabled = modals.bulkUpdate.ntfyEnabled;
		if (modals.bulkUpdate.emailEnabled !== null) payload.emailEnabled = modals.bulkUpdate.emailEnabled;
		if (modals.bulkUpdate.discordEnabled !== null) payload.discordEnabled = modals.bulkUpdate.discordEnabled;

		if (Object.keys(payload).length <= 1) {
			toast.error('Select at least one channel', { position: 'bottom-center' });
			return;
		}

		busy.delete = true;
		const response = await bulkUpdateNotificationRules(payload);
		busy.delete = false;

		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to update notification rules', { position: 'bottom-center' });
			modals.bulkUpdate.open = false;
			return;
		}

		toast.success(`${modals.bulkUpdate.ids.length} notification rules updated`, { position: 'bottom-center' });
		modals.bulkUpdate.open = false;
		await rulesResource.refetch();
		activeRows = null;
	}

	function openTestModal() {
		const firstTemplate = createTemplateOptions[0]?.key || '';
		modals.test = {
			open: true,
			templateKey: firstTemplate,
			targetKey: '',
			condition: ''
		};
	}

	let testTargetOptions = $derived.by(() => {
		const template = (currentConfig.templates || []).find(
			(t) => t.key === modals.test.templateKey
		);
		return (template?.targets || []).map((t) => ({ key: t.key, label: t.label }));
	});

	let testConditionOptions = $derived.by(() => {
		switch (modals.test.templateKey) {
			case 'system.disk.smart.temperature':
				return [
					{ key: 'temperature_warning', label: 'Warning (60 °C)' },
					{ key: 'temperature_critical', label: 'Critical (70 °C)' }
				];
			case 'system.disk.smart.wearout':
				return [
					{ key: 'wearout_warning', label: 'Warning (85.0%)' },
					{ key: 'wearout_critical', label: 'Critical (95.0%)' }
				];
			case 'system.disk.smart.health':
				return [
					{ key: 'health_failed', label: 'SMART Health Failed' },
					{ key: 'sector_issues', label: 'Sector Issues' }
				];
			case 'system.disk.smart.nvme':
				return [{ key: 'nvme_warning', label: 'NVMe Warning' }];
			case 'system.disk.smart.selftest':
				return [
					{ key: 'self_test_failed', label: 'Self-Test Failed' },
					{ key: 'self_test_passed', label: 'Self-Test Passed' }
				];
			case 'system.zfs.pool_state':
				return [
					{ key: 'pool_degraded', label: 'Degraded' },
					{ key: 'pool_faulted', label: 'Faulted' }
				];
			default:
				return [];
		}
	});

	$effect(() => {
		if (!modals.test.open) return;
		if (testTargetOptions.length > 0 && !testTargetOptions.some((t) => t.key === modals.test.targetKey)) {
			modals.test.targetKey = testTargetOptions[0].key;
		}
		if (
			modals.test.condition &&
			!testConditionOptions.some((c) => c.key === modals.test.condition)
		) {
			modals.test.condition = testConditionOptions[0]?.key || '';
		} else if (!modals.test.condition) {
			modals.test.condition = testConditionOptions[0]?.key || '';
		}
	});

	async function sendTest() {
		if (busy.test) return;
		if (!modals.test.templateKey) {
			toast.error('Select a template', { position: 'bottom-center' });
			return;
		}
		if (!modals.test.targetKey) {
			toast.error('Select an available target', { position: 'bottom-center' });
			return;
		}

		busy.test = true;
		const response = await testNotificationRule({
			templateKey: modals.test.templateKey,
			targetKey: modals.test.targetKey || undefined,
			condition: modals.test.condition || undefined
		});
		busy.test = false;

		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to send test notification', { position: 'bottom-center' });
			return;
		}

		toast.success('Test notification sent', { position: 'bottom-center' });
		modals.test.open = false;
		await rulesResource.refetch();
	}
</script>

<div class="flex h-full w-full flex-col overflow-hidden">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button size="sm" class="h-6" onclick={openCreateModal}>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>
		<Button size="sm" variant="outline" class="h-6.5" onclick={openTestModal}>
			<SpanWithIcon icon="icon-[mdi--flask-outline]" size="h-4 w-4" gap="gap-2" title="Test" />
		</Button>
		{#if selectedRule}
			<Button size="sm" variant="outline" class="h-6.5" onclick={openEditModal}>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
			</Button>
			<Button size="sm" variant="outline" class="h-6.5" onclick={openDeleteModal}>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
		{#if activeRows && activeRows.filter((r) => !(r as RuleRow).isTemplateRow && (r as RuleRow).ruleId).length > 1}
			<Button size="sm" variant="outline" class="h-6.5" onclick={openBulkUpdateModal}>
				<SpanWithIcon icon="icon-[material-symbols--toggle-on]" size="h-4 w-4" gap="gap-2" title="Bulk Update" />
			</Button>
			<Button size="sm" variant="outline" class="h-6.5" onclick={openBulkDeleteModal}>
				<SpanWithIcon icon="icon-[material-symbols--delete-sweep]" size="h-4 w-4" gap="gap-2" title="Bulk Delete" />
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
		multipleSelect={true}
		customPlaceholder="No notification rules available"
		selectableRowCheck={(row) => {
			const d = row.getData() as RuleRow;
			return !d.isTemplateRow;
		}}
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
						<CustomCheckbox label="Discord" bind:checked={modals.create.discordEnabled} />
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
			class="sm:max-w-150"
			onInteractOutside={(e) => e.preventDefault()}
			showCloseButton={true}
			showResetButton={true}
			onClose={() => (modals.edit.open = false)}
			onReset={() => {
				modals.edit.uiEnabled = editOriginal.uiEnabled;
				modals.edit.ntfyEnabled = editOriginal.ntfyEnabled;
				modals.edit.emailEnabled = editOriginal.emailEnabled;
				modals.edit.discordEnabled = editOriginal.discordEnabled;
				modals.edit.config = editOriginal.config;
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

			<div class="min-w-0 rounded-md border">
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
							<Table.Cell class="truncate max-w-0" title={modals.edit.targetLabel}>{modals.edit.targetLabel}</Table.Cell>
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

			{#if editTemplateHasConfig}
				<div class="space-y-2 pt-1">
					<p class="text-sm font-medium">
						{modals.edit.templateKey === 'system.disk.smart.temperature'
							? 'Temperature Thresholds'
							: 'Wear-Out Thresholds'}
					</p>
					<div class="flex min-w-0 items-start gap-3">
						<div class="flex min-w-0 flex-1 flex-col gap-1">
							<label class="text-muted-foreground text-xs">
								Warning{modals.edit.templateKey === 'system.disk.smart.temperature' ? ' °C' : ' %'}
							</label>
							<input
								type="number"
								min="0"
								max={modals.edit.templateKey === 'system.disk.smart.temperature' ? '125' : '100'}
								class="border-input bg-background ring-offset-background flex h-9 w-full rounded-md border px-3 py-1 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
								value={configNumber(
									modals.edit.templateKey === 'system.disk.smart.temperature'
										? 'warningCelsius'
										: 'warningPercent',
									modals.edit.templateKey === 'system.disk.smart.temperature'
										? editConfigDefaults['warningCelsius'] ?? 55
										: editConfigDefaults['warningPercent'] ?? 80
								)}
								oninput={(e) =>
									setConfigNumber(
										modals.edit.templateKey === 'system.disk.smart.temperature'
											? 'warningCelsius'
											: 'warningPercent',
										parseInt(e.currentTarget.value) || 0
									)}
							/>
							<span class="text-muted-foreground text-[10px]"
								>Default: {modals.edit.templateKey === 'system.disk.smart.temperature'
									? editConfigDefaults['warningCelsius'] ?? 55
									: editConfigDefaults['warningPercent'] ?? 80}{modals.edit.templateKey === 'system.disk.smart.temperature' ? ' °C' : ' %'}</span
							>
						</div>
						<div class="flex min-w-0 flex-1 flex-col gap-1">
							<label class="text-muted-foreground text-xs">
								Critical{modals.edit.templateKey === 'system.disk.smart.temperature' ? ' °C' : ' %'}
							</label>
							<input
								type="number"
								min="0"
								max={modals.edit.templateKey === 'system.disk.smart.temperature' ? '125' : '100'}
								class="border-input bg-background ring-offset-background flex h-9 w-full rounded-md border px-3 py-1 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
								value={configNumber(
									modals.edit.templateKey === 'system.disk.smart.temperature'
										? 'criticalCelsius'
										: 'criticalPercent',
									modals.edit.templateKey === 'system.disk.smart.temperature'
										? editConfigDefaults['criticalCelsius'] ?? 65
										: editConfigDefaults['criticalPercent'] ?? 90
								)}
								oninput={(e) =>
									setConfigNumber(
										modals.edit.templateKey === 'system.disk.smart.temperature'
											? 'criticalCelsius'
											: 'criticalPercent',
										parseInt(e.currentTarget.value) || 0
									)}
							/>
							<span class="text-muted-foreground text-[10px]"
								>Default: {modals.edit.templateKey === 'system.disk.smart.temperature'
									? editConfigDefaults['criticalCelsius'] ?? 65
									: editConfigDefaults['criticalPercent'] ?? 90}{modals.edit.templateKey === 'system.disk.smart.temperature' ? ' °C' : ' %'}</span
							>
						</div>
					</div>
					{#if editConfigInvalid}
						<p class="text-destructive text-xs">{editConfigInvalidMessage}</p>
					{/if}
				</div>
			{/if}

			<div class="space-y-2 pt-1">
				<p class="text-sm font-medium">Channels</p>
				<div class="flex items-center gap-4">
					<CustomCheckbox label="In-App" bind:checked={modals.edit.uiEnabled} />
					<CustomCheckbox label="ntfy" bind:checked={modals.edit.ntfyEnabled} />
					<CustomCheckbox label="Email" bind:checked={modals.edit.emailEnabled} />
					<CustomCheckbox label="Discord" bind:checked={modals.edit.discordEnabled} />
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

{#if modals.test.open}
	<Dialog.Root bind:open={modals.test.open}>
		<Dialog.Content
			class="sm:max-w-106.25"
			onInteractOutside={(e) => e.preventDefault()}
			showCloseButton={true}
			onClose={() => (modals.test.open = false)}
		>
			<Dialog.Header>
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[mdi--flask-outline]"
						size="h-5 w-5"
						gap="gap-2"
						title="Test Notification Rule"
					/>
				</Dialog.Title>
			</Dialog.Header>

			<div class="space-y-3">
				<SimpleSelect
					label="Template"
					options={createTemplateOptions.map((t) => ({ value: t.key, label: t.label }))}
					bind:value={modals.test.templateKey}
					onChange={(v) => (modals.test.templateKey = v)}
					classes={{
						parent: 'space-y-1',
						label: 'text-sm font-medium',
						trigger: 'inline-flex h-9 w-full items-center overflow-hidden px-3 text-left'
					}}
				/>

				<SimpleSelect
					label="Target"
					options={testTargetOptions.map((t) => ({ value: t.key, label: t.label }))}
					bind:value={modals.test.targetKey}
					onChange={(v) => (modals.test.targetKey = v)}
					disabled={testTargetOptions.length === 0}
					classes={{
						parent: 'space-y-1',
						label: 'text-sm font-medium',
						trigger: 'inline-flex h-9 w-full items-center overflow-hidden px-3 text-left'
					}}
				/>

				{#if testConditionOptions.length > 0}
					<SimpleSelect
						label="Condition"
						options={testConditionOptions.map((c) => ({ value: c.key, label: c.label }))}
						bind:value={modals.test.condition}
						onChange={(v) => (modals.test.condition = v)}
						classes={{
							parent: 'space-y-1',
							label: 'text-sm font-medium',
							trigger: 'inline-flex h-9 w-full items-center overflow-hidden px-3 text-left'
						}}
					/>
				{/if}
			</div>

			<Dialog.Footer>
				<Button
					onclick={sendTest}
					size="sm"
					disabled={busy.test || !modals.test.templateKey || !modals.test.targetKey}
				>
					{#if busy.test}
						<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>
					{/if}
					Send Test
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

<AlertDialog
	open={modals.bulkDelete.open}
	customTitle={`This will permanently delete <b>${modals.bulkDelete.ids.length}</b> notification rule(s).`}
	actions={{
		onConfirm: async () => {
			await bulkDeleteRules();
		},
		onCancel: () => {
			modals.bulkDelete.open = false;
		}
	}}
/>

{#if modals.bulkUpdate.open}
	<Dialog.Root bind:open={modals.bulkUpdate.open}>
		<Dialog.Content
			class="sm:max-w-150"
			onInteractOutside={(e) => e.preventDefault()}
			showCloseButton={true}
			onClose={() => (modals.bulkUpdate.open = false)}
		>
			<Dialog.Header>
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[material-symbols--toggle-on]"
						size="h-5 w-5"
						gap="gap-2"
						title="Bulk Update Notification Channels"
					/>
				</Dialog.Title>
			</Dialog.Header>

			<div class="space-y-4">
				<div class="rounded-md border bg-muted/10">
					<Accordion.Root type="single" collapsible>
						<Accordion.Item value="selected-rules" class="border-b-0">
							<Accordion.Trigger
								class="px-4 py-2.5 text-xs uppercase tracking-widest text-muted-foreground hover:no-underline"
							>
								{modals.bulkUpdate.ids.length} Rule(s) Selected
							</Accordion.Trigger>
							<Accordion.Content class="px-4 pb-3">
								<div class="space-y-1">
									{#each bulkUpdateGroups as group (group.label)}
										<div class="flex items-center justify-between rounded bg-background px-3 py-1.5 text-xs">
											<span class="text-muted-foreground">{group.label}</span>
											<span class="font-medium">{group.count} rule{group.count !== 1 ? 's' : ''}</span>
										</div>
									{/each}
								</div>
							</Accordion.Content>
						</Accordion.Item>
					</Accordion.Root>
				</div>

				<div class="grid grid-cols-2 gap-3">
					{#each [
						{ key: 'uiEnabled', label: 'In-App', icon: 'icon-[mdi--monitor]' },
						{ key: 'ntfyEnabled', label: 'ntfy', icon: 'icon-[mdi--bell]' },
						{ key: 'emailEnabled', label: 'Email', icon: 'icon-[mdi--email-outline]' },
						{ key: 'discordEnabled', label: 'Discord', icon: 'icon-[mdi--discord]' }
					] as channel (channel.key)}
						{@const value = modals.bulkUpdate[channel.key]}
						<div class="rounded-md border p-3 space-y-2.5">
							<div class="flex items-center gap-1.5 text-sm font-medium text-muted-foreground">
								<span class="{channel.icon} h-4 w-4 shrink-0"></span>
								{channel.label}
							</div>
							<div class="flex gap-2">
								<button
									class="inline-flex h-7 cursor-pointer items-center rounded-md border px-3 text-xs font-medium transition-all {value === true ? 'border-green-600 bg-green-600 text-white shadow-xs' : 'bg-background text-muted-foreground hover:bg-muted'} disabled:pointer-events-none disabled:opacity-50"
									onclick={() => (modals.bulkUpdate[channel.key] = true)}
								>
									Enable
								</button>
								<button
									class="inline-flex h-7 cursor-pointer items-center rounded-md border px-3 text-xs font-medium transition-all {value === false ? 'border-red-600 bg-red-600 text-white shadow-xs' : 'bg-background text-muted-foreground hover:bg-muted'} disabled:pointer-events-none disabled:opacity-50"
									onclick={() => (modals.bulkUpdate[channel.key] = false)}
								>
									Disable
								</button>
								<button
									class="inline-flex h-7 cursor-pointer items-center rounded-md border px-3 text-xs font-medium transition-all {value === null ? 'bg-muted text-foreground shadow-xs' : 'bg-background text-muted-foreground hover:bg-muted'} disabled:pointer-events-none disabled:opacity-50"
									onclick={() => (modals.bulkUpdate[channel.key] = null)}
								>
									No Change
								</button>
							</div>
						</div>
					{/each}
				</div>
			</div>

			<Dialog.Footer>
				<Button onclick={bulkUpdateRules} size="sm" disabled={busy.delete}>
					{#if busy.delete}
						<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>
					{/if}
					Apply Changes
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}
