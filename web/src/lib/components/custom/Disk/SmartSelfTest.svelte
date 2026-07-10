<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import {
		abortSmartSelfTest,
		createSmartSelfTestSchedule,
		deleteSmartSelfTestSchedule,
		getSmartSelfTest,
		listSmartSelfTestSchedules,
		updateSmartSelfTestSchedule,
		startSmartSelfTest
	} from '$lib/api/disk/disk';
	import ModalTable from '$lib/components/custom/ModalTable.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import type { APIResponse } from '$lib/types/common';
	import type {
		Disk,
		SmartSelfTestCapabilities,
		SmartSelfTestDetails,
		SmartSelfTestResult,
		SmartSelfTestSchedule,
		SmartSelfTestScheduleInput,
		SmartSelfTestScheduleType,
		SmartSelfTestStartKind
	} from '$lib/types/disk/disk';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { convertDbTime, cronToHuman } from '$lib/utils/time';
	import { IsDocumentVisible, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent, ColumnDefinition } from 'tabulator-tables';

	interface Props {
		open: boolean;
		disk: Disk;
		knownDiskUUIDs: string[];
	}

	type TestDefinition = {
		kind: SmartSelfTestStartKind;
		capability: 'short' | 'extended' | 'conveyance';
		label: string;
		description: string;
	};

	type SchedulePreset = {
		label: string;
		cronExpr: string;
	};

	type ScheduleForm = {
		id: number | null;
		testType: SmartSelfTestScheduleType;
		cronExpr: string;
		enabled: boolean;
	};

	let { open = $bindable(), disk, knownDiskUUIDs = [] }: Props = $props();
	let visible = new IsDocumentVisible();
	let details = $state<SmartSelfTestDetails | null>(null);
	let loading = $state(false);
	let refreshing = $state(false);
	let errorMessage = $state('');
	let activeTab = $state('run');
	let selectedTest = $state<SmartSelfTestStartKind | ''>('');
	let startConfirmOpen = $state(false);
	let abortConfirmOpen = $state(false);
	let action = $state<'start' | 'abort' | ''>('');
	let schedules = $state<SmartSelfTestSchedule[]>([]);
	let schedulesLoading = $state(false);
	let schedulesRefreshing = $state(false);
	let schedulesErrorMessage = $state('');
	let scheduleFormOpen = $state(false);
	let scheduleComboboxOpen = $state(false);
	let scheduleAction = $state<'save' | 'disable' | 'delete' | ''>('');
	let disabledScheduleID = $state<number | null>(null);
	let deleteSchedule = $state<SmartSelfTestSchedule | null>(null);
	let deleteConfirmOpen = $state(false);
	let viewGeneration = 0;
	let detailsRequest = 0;
	let schedulesRequest = 0;
	let scheduleForm = $state<ScheduleForm>({
		id: null,
		testType: 'short',
		cronExpr: '0 2 * * *',
		enabled: true
	});

	const testDefinitions: TestDefinition[] = [
		{
			kind: 'short',
			capability: 'short',
			label: 'Short',
			description: 'A brief device diagnostic intended for routine checks.'
		},
		{
			kind: 'extended',
			capability: 'extended',
			label: 'Extended',
			description: 'A thorough media diagnostic that can run for several hours.'
		},
		{
			kind: 'conveyance',
			capability: 'conveyance',
			label: 'Conveyance',
			description: 'Checks for damage that may have occurred while the device was transported.'
		}
	];

	const schedulePresets: Record<SmartSelfTestScheduleType, SchedulePreset[]> = {
		short: [
			{ label: 'Daily at 02:00', cronExpr: '0 2 * * *' },
			{ label: 'Weekly on Sunday at 02:00', cronExpr: '0 2 * * 0' },
			{ label: 'Every hour', cronExpr: '0 * * * *' },
			{ label: 'Every minute', cronExpr: '* * * * *' }
		],
		extended: [
			{ label: 'Weekly on Sunday at 03:00', cronExpr: '0 3 * * 0' },
			{ label: 'Monthly on day 1 at 03:00', cronExpr: '0 3 1 * *' },
			{ label: 'Daily at 03:00', cronExpr: '0 3 * * *' },
			{ label: 'Every 6 hours', cronExpr: '0 */6 * * *' },
			{ label: 'Every minute', cronExpr: '* * * * *' }
		]
	};

	let supportedTests = $derived.by(() => {
		if (!details) return [];
		return testDefinitions.filter(
			(definition) => details?.capabilities[definition.capability] === true
		);
	});

	let knownDiskUUIDSet = $derived(new Set(knownDiskUUIDs));
	let diskSchedules = $derived(
		schedules.filter(
			(schedule) => schedule.diskKey === disk.uuid && knownDiskUUIDSet.has(schedule.diskKey)
		)
	);
	let unavailableDiskSchedules = $derived(
		schedules.filter((schedule) => !knownDiskUUIDSet.has(schedule.diskKey))
	);
	let enabledDiskScheduleCount = $derived(
		diskSchedules.filter((schedule) => schedule.enabled).length
	);

	function scheduleStatusIsActive(status: string): boolean {
		return status === 'starting' || status === 'running';
	}

	let activeDiskSchedule = $derived(
		diskSchedules.find((schedule) => scheduleStatusIsActive(schedule.lastStatus)) || null
	);
	let activeSchedule = $derived(activeDiskSchedule !== null);
	let queuedSchedule = $derived(diskSchedules.some((schedule) => schedule.lastStatus === 'queued'));
	let manualStartBlocked = $derived(activeSchedule || queuedSchedule);
	let activeUnavailableSchedule = $derived(
		unavailableDiskSchedules.some((schedule) => scheduleStatusIsActive(schedule.lastStatus))
	);

	let selectedActiveState = $derived(
		details?.status.running === true || details?.status.state === 'ambiguous' || activeSchedule
	);
	let activeState = $derived(selectedActiveState || activeUnavailableSchedule);

	let supportedScheduleTypes = $derived.by(() => {
		const values: SmartSelfTestScheduleType[] = [];
		if (details?.capabilities.short) values.push('short');
		if (details?.capabilities.extended) values.push('extended');
		return values;
	});

	let availableScheduleTypes = $derived(
		disk.identityStable
			? supportedScheduleTypes.filter(
					(testType) => !diskSchedules.some((schedule) => schedule.testType === testType)
				)
			: []
	);

	let currentSchedulePresets = $derived(schedulePresets[scheduleForm.testType]);
	let currentScheduleOptions = $derived(
		currentSchedulePresets.map((preset) => ({ value: preset.cronExpr, label: preset.label }))
	);
	let scheduleCronDescription = $derived(cronToHuman(scheduleForm.cronExpr));
	let scheduleCronInvalid = $derived(
		scheduleForm.cronExpr.trim() === '' || scheduleCronDescription === ''
	);
	let editingSchedule = $derived(
		scheduleForm.id === null
			? null
			: diskSchedules.find((schedule) => schedule.id === scheduleForm.id) || null
	);
	let scheduleEditLocked = $derived(
		editingSchedule !== null && scheduleStatusIsActive(editingSchedule.lastStatus)
	);
	let deleteScheduleLocked = $derived.by(() => {
		const selected = deleteSchedule;
		if (!selected) return false;
		const current = schedules.find((schedule) => schedule.id === selected.id);
		return scheduleStatusIsActive(current?.lastStatus || selected.lastStatus);
	});

	let selectedDefinition = $derived(
		testDefinitions.find((definition) => definition.kind === selectedTest) || null
	);

	let latestResult = $derived(details?.status.results[0] || null);

	let progressPercent = $derived.by(() => {
		const status = details?.status;
		if (!status?.running) return null;
		if (status.progress_known && status.progress_pct >= 0) {
			return Math.min(100, Math.max(0, status.progress_pct));
		}
		if (status.remaining_known && status.remaining_pct >= 0) {
			return Math.min(100, Math.max(0, 100 - status.remaining_pct));
		}
		return null;
	});
	let currentProgressPercent = $derived(
		details?.status.running
			? progressPercent
			: activeDiskSchedule?.progressKnown && activeDiskSchedule.progressPct >= 0
				? Math.min(100, Math.max(0, activeDiskSchedule.progressPct))
				: null
	);

	function durationMinutes(kind: string, capabilities?: SmartSelfTestCapabilities): number {
		if (!capabilities) return 0;
		switch (kind) {
			case 'offline':
				return capabilities.offline_duration_minutes;
			case 'short':
				return capabilities.short_duration_minutes;
			case 'extended':
				return capabilities.extended_duration_minutes;
			case 'conveyance':
				return capabilities.conveyance_duration_minutes;
			default:
				return 0;
		}
	}

	let selectedDuration = $derived(durationMinutes(selectedTest, details?.capabilities));

	let runningDuration = $derived(
		activeDiskSchedule?.estimatedMinutes ||
			details?.status.estimated_duration_minutes ||
			durationMinutes(details?.status.type || '', details?.capabilities)
	);

	function formatDuration(minutes: number): string {
		if (minutes <= 0) return 'Not reported by the device';
		if (minutes < 60) return `${minutes} minute${minutes === 1 ? '' : 's'}`;
		const hours = Math.floor(minutes / 60);
		const remainder = minutes % 60;
		if (remainder === 0) return `${hours} hour${hours === 1 ? '' : 's'}`;
		return `${hours}h ${remainder}m`;
	}

	function formatLabel(value: string): string {
		if (!value) return 'Unknown';
		return value
			.trim()
			.replaceAll('_', ' ')
			.replace(/\b\w/g, (character) => character.toUpperCase());
	}

	function stateClass(state: string): string {
		switch (state) {
			case 'starting':
			case 'running':
				return 'border-blue-500/40 bg-blue-500/10 text-blue-600 dark:text-blue-300';
			case 'ambiguous':
				return 'border-yellow-500/40 bg-yellow-500/10 text-yellow-700 dark:text-yellow-300';
			default:
				return 'text-muted-foreground';
		}
	}

	function outcomeClass(result: SmartSelfTestResult): string {
		const outcome = result.outcome.toLowerCase();
		const status = result.status.toLowerCase();
		if (
			outcome === 'failed' ||
			status.startsWith('failed') ||
			status === 'fatal' ||
			status === 'unknown_error' ||
			status === 'completed_segment_failed'
		) {
			return 'border-red-500/40 bg-red-500/10 text-red-700 dark:text-red-300';
		}
		if (
			outcome === 'aborted' ||
			status.startsWith('aborted') ||
			status === 'interrupted' ||
			status === 'cancelled'
		) {
			return 'border-yellow-500/40 bg-yellow-500/10 text-yellow-700 dark:text-yellow-300';
		}
		if (outcome === 'in_progress' || status === 'in_progress' || status === 'running') {
			return 'border-blue-500/40 bg-blue-500/10 text-blue-600 dark:text-blue-300';
		}
		if (outcome === 'passed' || status === 'completed') {
			return 'border-green-500/40 bg-green-500/10 text-green-700 dark:text-green-300';
		}
		return 'text-muted-foreground';
	}

	function resultOutcome(result: SmartSelfTestResult): string {
		return formatLabel(result.outcome || result.status);
	}

	function resultLifetimeHours(result: SmartSelfTestResult): string {
		return result.lifetime_hours_exact.trim() || result.lifetime_hours.toString();
	}

	function resultLocation(result: SmartSelfTestResult): string {
		const values: string[] = [];
		if (result.nsid_valid) values.push(`NSID ${result.nsid}`);
		if (result.lba_valid) values.push(`LBA ${result.lba_exact.trim() || result.lba.toString()}`);
		if (values.length > 0) return values.join(' · ');
		if (result.outcome === 'passed' || result.status === 'completed') return 'No errors';
		return 'Not reported';
	}

	function resultDetail(result: SmartSelfTestResult): string {
		const values: string[] = [];
		const inProgress = result.outcome === 'in_progress' || result.status === 'in_progress';
		if (result.status) values.push(formatLabel(result.status));
		if (result.segment_num > 0) values.push(`Segment ${result.segment_num}`);
		if (inProgress && result.remaining_pct === 0) values.push('Less than 10% remaining');
		if (inProgress && result.remaining_pct > 0) values.push(`${result.remaining_pct}% remaining`);
		if (!inProgress && result.remaining_pct > 0) {
			values.push(`${result.remaining_pct}% remaining when stopped`);
		}
		if (result.sense_key !== 0 || result.asc !== 0 || result.ascq !== 0) {
			values.push(
				`Sense ${result.sense_key.toString(16).padStart(2, '0')}/${result.asc
					.toString(16)
					.padStart(2, '0')}/${result.ascq.toString(16).padStart(2, '0')}`
			);
		}
		if (result.status_code_type_valid) values.push(`Status type ${result.status_code_type}`);
		if (result.status_code_valid) values.push(`Status code ${result.status_code}`);
		if (result.checkpoint > 0) values.push(`Checkpoint ${result.checkpoint}`);
		if (result.parameter_code !== 0)
			values.push(`Parameter 0x${result.parameter_code.toString(16)}`);
		if (result.vendor_specific !== 0)
			values.push(`Vendor 0x${result.vendor_specific.toString(16)}`);
		return values.join(' · ') || '-';
	}

	function resultMode(result: SmartSelfTestResult): string {
		if (result.mode) return formatLabel(result.mode);
		if ((result.protocol || details?.status.protocol).toLowerCase() === 'nvme') return 'N/A';
		return 'Not reported';
	}

	function resultType(result: SmartSelfTestResult): string {
		return formatLabel(result.type.replace(/_captive$/, ''));
	}

	function resultDate(result: SmartSelfTestResult): number {
		if (!result.started_at) return -1;
		const timestamp = Date.parse(result.started_at);
		return Number.isFinite(timestamp) ? timestamp : -1;
	}

	function dateFormatter(cell: CellComponent): string {
		const timestamp = Number(cell.getValue());
		return timestamp >= 0 ? convertDbTime(new Date(timestamp).toISOString()) : '-';
	}

	const historyColumns: ColumnDefinition[] = [
		{ title: '', field: 'sequence', visible: false, sorter: 'number' },
		{ title: 'Date', field: 'date', minWidth: 170, sorter: 'number', formatter: dateFormatter },
		{ title: 'Type', field: 'type', minWidth: 115 },
		{ title: 'Mode', field: 'mode', minWidth: 90 },
		{ title: 'Result', field: 'result', minWidth: 100 },
		{ title: 'Drive Hours', field: 'powerOnHours', minWidth: 110 },
		{ title: 'First Error', field: 'location', minWidth: 125, tooltip: true },
		{ title: 'Details', field: 'details', minWidth: 180, tooltip: true }
	];

	let historyRows = $derived(
		(details?.status.results || []).map((result, index) => ({
			id: `${result.type}-${result.status}-${resultLifetimeHours(result)}-${index}`,
			sequence: index,
			date: resultDate(result),
			type: resultType(result),
			mode: resultMode(result),
			result: resultOutcome(result),
			powerOnHours: `${resultLifetimeHours(result)} h`,
			location: resultLocation(result),
			details: resultDetail(result)
		}))
	);

	function scheduleStatusClass(status: string): string {
		switch (status) {
			case 'starting':
			case 'running':
				return 'border-blue-500/40 bg-blue-500/10 text-blue-600 dark:text-blue-300';
			case 'queued':
				return 'border-yellow-500/40 bg-yellow-500/10 text-yellow-700 dark:text-yellow-300';
			case 'passed':
				return 'border-green-500/40 bg-green-500/10 text-green-700 dark:text-green-300';
			case 'failed':
				return 'border-red-500/40 bg-red-500/10 text-red-700 dark:text-red-300';
			case 'aborted':
				return 'border-orange-500/40 bg-orange-500/10 text-orange-700 dark:text-orange-300';
			default:
				return 'text-muted-foreground';
		}
	}

	function formatScheduleTime(value: string | null, empty: string): string {
		return value ? convertDbTime(value) : empty;
	}

	function setScheduleType(testType: SmartSelfTestScheduleType) {
		const preset = schedulePresets[testType][0];
		scheduleForm.testType = testType;
		scheduleForm.cronExpr = preset.cronExpr;
	}

	function openNewSchedule() {
		const testType = availableScheduleTypes[0];
		if (!testType) return;
		const preset = schedulePresets[testType][0];
		scheduleForm = {
			id: null,
			testType,
			cronExpr: preset.cronExpr,
			enabled: true
		};
		scheduleComboboxOpen = false;
		scheduleFormOpen = true;
	}

	function openEditSchedule(schedule: SmartSelfTestSchedule) {
		if (scheduleStatusIsActive(schedule.lastStatus)) return;
		scheduleForm = {
			id: schedule.id,
			testType: schedule.testType,
			cronExpr: schedule.cronExpr,
			enabled: schedule.enabled
		};
		scheduleComboboxOpen = false;
		scheduleFormOpen = true;
	}

	function apiError(response: APIResponse, fallback: string): string {
		if (Array.isArray(response.error)) return response.error.join(', ');
		return response.error || response.message || fallback;
	}

	function isCurrentView(generation: number, device: string): boolean {
		return open && generation === viewGeneration && device === disk.device;
	}

	async function refreshSchedules(initial = false, generation = viewGeneration) {
		const device = disk.device;
		if (!device || !open || (schedulesRefreshing && !initial)) return;
		const request = ++schedulesRequest;
		schedulesRefreshing = true;
		if (initial) schedulesLoading = true;
		const response = await listSmartSelfTestSchedules();
		if (!isCurrentView(generation, device) || request !== schedulesRequest) return;
		if (Array.isArray(response)) {
			schedules = response;
			schedulesErrorMessage = '';
		} else {
			schedulesErrorMessage = apiError(response, 'Unable to load periodic self-test schedules');
		}
		schedulesLoading = false;
		schedulesRefreshing = false;
	}

	async function refreshAll(initial = false, generation = viewGeneration) {
		await Promise.all([refresh(initial, generation), refreshSchedules(initial, generation)]);
	}

	async function saveSchedule() {
		if (scheduleAction || scheduleCronInvalid || scheduleEditLocked) return;
		if (scheduleForm.id === null && !disk.identityStable) {
			toast.error('Periodic tests require a stable disk identity', {
				position: 'bottom-center'
			});
			return;
		}
		if (scheduleForm.id === null && !supportedScheduleTypes.includes(scheduleForm.testType)) {
			toast.error('This test type is not supported by the selected disk', {
				position: 'bottom-center'
			});
			return;
		}
		const generation = viewGeneration;
		const device = disk.device;
		const scheduleID = scheduleForm.id;
		const input: SmartSelfTestScheduleInput = {
			device,
			testType: scheduleForm.testType,
			cronExpr: scheduleForm.cronExpr.trim(),
			enabled: scheduleForm.enabled
		};
		scheduleAction = 'save';
		const response = scheduleID
			? await updateSmartSelfTestSchedule(scheduleID, input)
			: await createSmartSelfTestSchedule(input);
		if (!isCurrentView(generation, device)) return;
		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			const message = apiError(response, 'Unable to save the self-test schedule');
			toast.error(message, { position: 'bottom-center' });
			scheduleAction = '';
			return;
		}
		if (!isAPIResponse(response)) {
			const index = schedules.findIndex((schedule) => schedule.id === response.id);
			if (index >= 0) schedules[index] = response;
			else schedules.push(response);
		}
		toast.success(scheduleID ? 'Self-test schedule updated' : 'Self-test schedule created', {
			position: 'bottom-center'
		});
		scheduleFormOpen = false;
		scheduleAction = '';
		await refreshSchedules(false, generation);
	}

	async function disableUnavailableSchedule(schedule: SmartSelfTestSchedule) {
		if (
			scheduleAction ||
			!schedule.enabled ||
			scheduleStatusIsActive(schedule.lastStatus) ||
			!unavailableDiskSchedules.some((candidate) => candidate.id === schedule.id)
		) {
			return;
		}
		const generation = viewGeneration;
		const device = disk.device;
		scheduleAction = 'disable';
		disabledScheduleID = schedule.id;
		const response = await updateSmartSelfTestSchedule(schedule.id, {
			device: schedule.device,
			testType: schedule.testType,
			cronExpr: schedule.cronExpr,
			enabled: false
		});
		if (!isCurrentView(generation, device)) return;
		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			const message = apiError(response, 'Unable to disable the self-test schedule');
			toast.error(message, { position: 'bottom-center' });
			scheduleAction = '';
			disabledScheduleID = null;
			return;
		}
		if (!isAPIResponse(response)) {
			const index = schedules.findIndex((candidate) => candidate.id === response.id);
			if (index >= 0) schedules[index] = response;
		}
		toast.success(`Self-test schedule for ${schedule.device} disabled`, {
			position: 'bottom-center'
		});
		scheduleAction = '';
		disabledScheduleID = null;
		await refreshSchedules(false, generation);
	}

	async function confirmDeleteSchedule() {
		if (!deleteSchedule || deleteScheduleLocked || scheduleAction) return;
		const generation = viewGeneration;
		const device = disk.device;
		const scheduleID = deleteSchedule.id;
		scheduleAction = 'delete';
		const response = await deleteSmartSelfTestSchedule(scheduleID);
		if (!isCurrentView(generation, device)) return;
		if (response.status === 'error') {
			handleAPIError(response);
			const message = apiError(response, 'Unable to delete the self-test schedule');
			toast.error(message, { position: 'bottom-center' });
			scheduleAction = '';
			deleteConfirmOpen = false;
			return;
		}
		schedules = schedules.filter((schedule) => schedule.id !== scheduleID);
		deleteSchedule = null;
		deleteConfirmOpen = false;
		scheduleAction = '';
		toast.success('Self-test schedule deleted', { position: 'bottom-center' });
		await refreshSchedules(false, generation);
	}

	async function refresh(initial = false, generation = viewGeneration) {
		const device = disk.device;
		if (!device || !open || (refreshing && !initial)) return;
		const request = ++detailsRequest;
		refreshing = true;
		if (initial) loading = true;
		const response = await getSmartSelfTest(device);
		if (!isCurrentView(generation, device) || request !== detailsRequest) return;
		if (isAPIResponse(response)) {
			errorMessage = apiError(response, 'Unable to read S.M.A.R.T self-test status');
			if (initial) details = null;
		} else {
			details = response;
			errorMessage = '';
			const available = testDefinitions.filter(
				(definition) => response.capabilities[definition.capability] === true
			);
			if (!available.some((definition) => definition.kind === selectedTest)) {
				selectedTest = available[0]?.kind || '';
			}
		}
		loading = false;
		refreshing = false;
	}

	async function startTest() {
		if (!selectedTest || manualStartBlocked || selectedActiveState || action) return;
		const generation = viewGeneration;
		const device = disk.device;
		const testType = selectedTest;
		const testLabel = selectedDefinition?.label || 'S.M.A.R.T';
		action = 'start';
		const response = await startSmartSelfTest(device, testType);
		if (!isCurrentView(generation, device)) return;
		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			errorMessage = apiError(response, 'Unable to start the S.M.A.R.T test');
			toast.error(errorMessage, { position: 'bottom-center' });
		} else {
			if (!isAPIResponse(response)) details = response;
			errorMessage = '';
			toast.success(`${testLabel} test started on ${device}`, {
				position: 'bottom-center'
			});
		}
		startConfirmOpen = false;
		action = '';
		await refresh(false, generation);
	}

	async function abortTest() {
		if (action) return;
		const generation = viewGeneration;
		const device = disk.device;
		action = 'abort';
		const response = await abortSmartSelfTest(device);
		if (!isCurrentView(generation, device)) return;
		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			errorMessage = apiError(response, 'Unable to abort the S.M.A.R.T test');
			toast.error(errorMessage, { position: 'bottom-center' });
		} else {
			if (!isAPIResponse(response)) details = response;
			errorMessage = '';
			toast.success(`S.M.A.R.T test aborted on ${device}`, {
				position: 'bottom-center'
			});
		}
		abortConfirmOpen = false;
		action = '';
		await refresh(false, generation);
	}

	function pollActive() {
		if (!open || !visible.current || loading || action || scheduleAction) return;
		if (selectedActiveState) void refreshAll();
		else if (activeUnavailableSchedule) void refreshSchedules();
	}

	function pollIdle() {
		if (!open || !visible.current || loading || action || scheduleAction) return;
		if (!activeState) void refreshAll();
		else if (!selectedActiveState) void refresh();
	}

	watch([() => open, () => disk.device, () => disk.uuid], ([isOpen]) => {
		const generation = ++viewGeneration;
		detailsRequest++;
		schedulesRequest++;
		loading = false;
		refreshing = false;
		schedulesLoading = false;
		schedulesRefreshing = false;
		action = '';
		scheduleAction = '';
		disabledScheduleID = null;
		startConfirmOpen = false;
		abortConfirmOpen = false;
		deleteConfirmOpen = false;
		scheduleFormOpen = false;
		scheduleComboboxOpen = false;
		deleteSchedule = null;
		if (!isOpen) {
			return;
		}
		activeTab = 'run';
		details = null;
		errorMessage = '';
		schedulesErrorMessage = '';
		selectedTest = '';
		void refreshAll(true, generation);
	});

	useInterval(5000, { callback: pollActive });
	useInterval(30000, { callback: pollIdle });
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-5xl flex-col gap-0 overflow-hidden p-6 sm:h-[82dvh] sm:max-h-[680px] sm:w-[calc(100vw-4rem)] sm:max-w-5xl"
		showCloseButton={true}
	>
		<Dialog.Header class="pr-10">
			<Dialog.Title class="flex items-center gap-2">
				<SpanWithIcon
					icon="icon-[material-symbols--fact-check-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title="S.M.A.R.T Self-Test"
				/>
				<span class="text-muted-foreground">•</span>
				<span>{disk.device}</span>
				{#if details?.capabilities.protocol}
					<Badge variant="outline">{details.capabilities.protocol}</Badge>
				{/if}
				{#if details}
					<Badge
						variant="outline"
						class={stateClass(activeDiskSchedule?.lastStatus || details.status.state)}
					>
						{formatLabel(activeDiskSchedule?.lastStatus || details.status.state)}
					</Badge>
					{#if !selectedActiveState && latestResult}
						<Badge variant="outline" class={outcomeClass(latestResult)}>
							Last: {resultOutcome(latestResult)}
						</Badge>
					{/if}
				{/if}
			</Dialog.Title>
			<Dialog.Description>
				{disk.model || 'Disk diagnostic status and result history'}
			</Dialog.Description>
		</Dialog.Header>

		<Tabs.Root bind:value={activeTab} class="mt-4 flex min-h-0 flex-1 flex-col">
			<Tabs.List class="grid w-full shrink-0 grid-cols-3 p-0">
				<Tabs.Trigger class="border-b" value="run">
					<span class="icon-[material-symbols--play-circle-outline] h-4 w-4"></span>
					Run Test
				</Tabs.Trigger>
				<Tabs.Trigger class="border-b" value="history">
					<span class="icon-[material-symbols--history] h-4 w-4"></span>
					History
				</Tabs.Trigger>
				<Tabs.Trigger class="border-b" value="schedule">
					<span class="icon-[material-symbols--calendar-clock-outline] h-4 w-4"></span>
					Schedule
					{#if enabledDiskScheduleCount > 0}
						<Badge variant="outline" class="ml-1 px-1.5 py-0 text-[10px]">
							{enabledDiskScheduleCount}
						</Badge>
					{/if}
				</Tabs.Trigger>
			</Tabs.List>

			<div class="flex min-h-0 flex-1 flex-col">
				<Tabs.Content
					value="run"
					class="m-0 flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-4"
				>
					{#if loading && !details}
						<div class="text-muted-foreground flex h-48 items-center justify-center gap-2">
							<span class="icon-[mdi--loading] h-5 w-5 animate-spin"></span>
							<span>Reading self-test capabilities and status</span>
						</div>
					{:else if errorMessage && !details}
						<div
							class="border-destructive/40 bg-destructive/10 text-destructive rounded-md border p-4"
						>
							<div class="flex items-start gap-3">
								<span class="icon-[mdi--alert-circle-outline] mt-0.5 h-5 w-5 shrink-0"></span>
								<div class="min-w-0 flex-1 space-y-3">
									<p>{errorMessage}</p>
									<Button size="sm" variant="outline" onclick={() => refresh(true)}>Retry</Button>
								</div>
							</div>
						</div>
					{:else if details}
						{#if errorMessage}
							<div
								class="border-yellow-500/40 bg-yellow-500/10 rounded-md border p-3 text-sm text-yellow-700 dark:text-yellow-300"
							>
								{errorMessage}
							</div>
						{/if}

						{#if selectedActiveState}
							<div class="flex min-h-0 flex-1 flex-col rounded-md border p-5">
								<div class="flex items-center gap-2">
									<span class="icon-[mdi--progress-check] text-primary h-5 w-5"></span>
									<span class="font-semibold">Test in progress</span>
								</div>

								<div class="mt-5 space-y-5">
									<div class="grid gap-3 text-sm sm:grid-cols-3">
										<div>
											<p class="text-muted-foreground text-xs">Execution</p>
											<p class="font-medium">
												{formatLabel(
													activeDiskSchedule?.lastStatus || details.status.execution_status
												)}
											</p>
										</div>
										<div>
											<p class="text-muted-foreground text-xs">Test Type</p>
											<p class="font-medium">
												{formatLabel(activeDiskSchedule?.testType || details.status.type)}
											</p>
										</div>
										<div>
											<p class="text-muted-foreground text-xs">Estimated Duration</p>
											<p class="font-medium">{formatDuration(runningDuration)}</p>
										</div>
									</div>

									<div class="space-y-2">
										<div class="flex items-center justify-between text-sm">
											<span>Test progress</span>
											<span class="text-muted-foreground">
												{currentProgressPercent === null
													? details.capabilities.progress
														? 'Waiting for device'
														: 'Not reported by device'
													: `${currentProgressPercent}%`}
											</span>
										</div>
										<div class="bg-secondary h-2.5 w-full overflow-hidden rounded-full">
											{#if currentProgressPercent !== null}
												<div
													class="h-full rounded-full bg-blue-500 transition-[width]"
													style="width: {currentProgressPercent}%"
													role="progressbar"
													aria-valuemin="0"
													aria-valuemax="100"
													aria-valuenow={currentProgressPercent}
												></div>
											{/if}
										</div>
										<p class="text-muted-foreground text-xs">
											The test continues on the device if this window is closed.
										</p>
										{#if (details.status.running || details.status.state === 'ambiguous') && details.capabilities.abort}
											<div class="flex justify-end pt-2">
												<Button
													size="sm"
													variant="destructive"
													disabled={action !== ''}
													onclick={() => (abortConfirmOpen = true)}
												>
													{#if action === 'abort'}
														<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
													{/if}
													Abort Test
												</Button>
											</div>
										{/if}
									</div>
								</div>
							</div>
						{/if}

						{#if !selectedActiveState}
							<div
								class="grid min-h-[390px] flex-1 overflow-hidden rounded-md border lg:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]"
							>
								<div class="space-y-4 border-b p-5 lg:border-r lg:border-b-0">
									<div class="flex items-center justify-between gap-3">
										<div>
											<p class="font-semibold">Choose a test</p>
											<p class="text-muted-foreground text-sm">Only supported tests are shown.</p>
										</div>
										<Button
											size="sm"
											variant="ghost"
											disabled={refreshing ||
												schedulesRefreshing ||
												action !== '' ||
												scheduleAction !== ''}
											onclick={() => refreshAll()}
											title="Refresh status"
										>
											<span
												class={refreshing || schedulesRefreshing
													? 'icon-[mdi--loading] h-4 w-4 animate-spin'
													: 'icon-[mdi--refresh] h-4 w-4'}
											></span>
										</Button>
									</div>

									{#if !details.capabilities.supported}
										<div class="text-muted-foreground flex items-center gap-2 text-sm">
											<span class="icon-[material-symbols--info-outline] h-4 w-4"></span>
											<span>This device does not report self-test support.</span>
										</div>
									{:else if supportedTests.length === 0}
										<div class="text-muted-foreground flex items-center gap-2 text-sm">
											<span class="icon-[material-symbols--info-outline] h-4 w-4"></span>
											<span>No non-blocking self-test types are available for this device.</span>
										</div>
									{:else}
										<div class="space-y-2">
											{#each supportedTests as test (test.kind)}
												<button
													type="button"
													class="w-full rounded-md border p-3 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-50 {selectedTest ===
													test.kind
														? 'border-primary bg-primary/5'
														: 'hover:bg-muted'}"
													disabled={manualStartBlocked}
													onclick={() => (selectedTest = test.kind)}
												>
													<div class="flex items-center justify-between gap-2">
														<div class="flex items-center gap-2">
															<span class="font-medium">{test.label}</span>
															{#if test.kind === 'short'}
																<Badge variant="outline" class="px-1.5 py-0 text-[10px]"
																	>Recommended</Badge
																>
															{/if}
														</div>
														{#if selectedTest === test.kind}
															<span class="icon-[mdi--check-circle] text-primary h-4 w-4"></span>
														{/if}
													</div>
													<p class="text-muted-foreground mt-1 text-xs">
														{formatDuration(durationMinutes(test.kind, details.capabilities))}
													</p>
												</button>
											{/each}
										</div>
									{/if}
								</div>

								<div class="flex flex-col justify-between gap-8 p-6">
									{#if selectedDefinition}
										<div class="space-y-6">
											<div class="space-y-2">
												<p
													class="text-muted-foreground text-xs font-medium tracking-wide uppercase"
												>
													Selected test
												</p>
												<div class="flex flex-wrap items-center gap-2">
													<h3 class="text-xl font-semibold">{selectedDefinition.label}</h3>
													{#if selectedDefinition.kind === 'short'}
														<Badge variant="outline">Recommended</Badge>
													{/if}
												</div>
												<p class="text-muted-foreground text-sm">
													{selectedDefinition.description}
												</p>
											</div>

											<div class="grid gap-4 sm:grid-cols-2">
												<div>
													<p class="text-muted-foreground text-xs">Estimated duration</p>
													<p class="font-medium">{formatDuration(selectedDuration)}</p>
												</div>
												<div>
													<p class="text-muted-foreground text-xs">Disk availability</p>
													<p class="font-medium">Remains online</p>
												</div>
											</div>

											<p class="text-muted-foreground text-sm">
												The diagnostic uses device resources and may temporarily reduce I/O
												performance.
											</p>

											{#if details.capabilities.protocol === 'SCSI' && !details.capabilities.execution_support_known}
												<p class="text-sm text-yellow-700 dark:text-yellow-300">
													Execution is unconfirmed for this device and the start command may be
													rejected.
												</p>
											{/if}

											{#if queuedSchedule}
												<p class="text-sm text-yellow-700 dark:text-yellow-300">
													A periodic test is queued. Manual starts become available when it leaves
													the queue.
												</p>
											{/if}
										</div>

										<Button
											class="self-start"
											disabled={manualStartBlocked || action !== ''}
											onclick={() => (startConfirmOpen = true)}
										>
											Start {selectedDefinition.label} Test
										</Button>
									{:else}
										<div
											class="text-muted-foreground flex h-full items-center justify-center text-sm"
										>
											No runnable self-test is available for this device.
										</div>
									{/if}
								</div>
							</div>
						{/if}
					{/if}
				</Tabs.Content>

				<Tabs.Content
					value="schedule"
					class="m-0 flex min-h-0 flex-1 flex-col overflow-y-auto p-4"
				>
					<div class="flex min-h-full flex-1 flex-col gap-4">
						<div class="flex items-center justify-between gap-3 px-1">
							<div class="flex items-center gap-2">
								<span class="icon-[material-symbols--calendar-clock-outline] text-primary h-5 w-5"
								></span>
								<span class="font-semibold">Periodic Tests</span>
							</div>
							{#if !schedulesLoading && !scheduleFormOpen && availableScheduleTypes.length > 0}
								<Button size="sm" variant="outline" onclick={openNewSchedule}>
									<span class="icon-[gg--add] h-4 w-4"></span>
									Add Schedule
								</Button>
							{/if}
						</div>

						<div class="flex flex-1 flex-col gap-4">
							{#if !disk.identityStable}
								<div
									class="border-yellow-500/40 bg-yellow-500/10 rounded-md border p-3 text-sm text-yellow-700 dark:text-yellow-300"
								>
									Periodic tests require a unique serial or LUN identity so the schedule follows the
									physical disk if its device name changes.
								</div>
							{/if}
							<div class="text-muted-foreground px-1 text-sm">
								<div class="flex items-start gap-2">
									<span
										class="icon-[material-symbols--info-outline] mt-0.5 h-4 w-4 shrink-0 text-blue-500"
									></span>
									<p>
										Schedules are opt-in and use the node's local time. A run waits safely if
										another disk test is active.
									</p>
								</div>
							</div>

							{#if schedulesErrorMessage}
								<div
									class="border-yellow-500/40 bg-yellow-500/10 rounded-md border p-3 text-sm text-yellow-700 dark:text-yellow-300"
								>
									<div class="flex items-center justify-between gap-3">
										<span
											>{schedulesErrorMessage}. The last successfully loaded schedules, if any, are
											retained below.</span
										>
										<Button
											size="sm"
											variant="outline"
											disabled={schedulesRefreshing || scheduleAction !== ''}
											onclick={() => refreshSchedules()}
										>
											Retry
										</Button>
									</div>
								</div>
							{/if}

							{#if scheduleFormOpen}
								<div class="space-y-4 rounded-md border p-4">
									<div class="flex items-center justify-between gap-2">
										<div class="flex items-center gap-2">
											<span class="icon-[material-symbols--edit-calendar-outline] text-primary h-4 w-4"
											></span>
											<p class="font-medium">
												{scheduleForm.id ? 'Edit Schedule' : 'New Schedule'}
											</p>
										</div>
									</div>

									{#if scheduleEditLocked}
										<div
											class="border-yellow-500/40 bg-yellow-500/10 rounded-md border p-3 text-sm text-yellow-700 dark:text-yellow-300"
										>
											This schedule is starting or running and cannot be edited until the test
											finishes.
										</div>
									{/if}

									<div class="grid gap-4 sm:grid-cols-2">
										<SimpleSelect
											label="Test Type"
											options={(scheduleForm.id !== null
												? [scheduleForm.testType]
												: availableScheduleTypes
											).map((testType) => ({ value: testType, label: formatLabel(testType) }))}
											bind:value={scheduleForm.testType}
											onChange={(value) => setScheduleType(value as SmartSelfTestScheduleType)}
											disabled={scheduleForm.id !== null ||
												scheduleEditLocked ||
												scheduleAction !== ''}
											classes={{
												parent: 'space-y-1',
												label: 'text-sm font-medium',
												trigger:
													'inline-flex h-9 w-full items-center overflow-hidden px-3 text-left'
											}}
										/>

										<div class="min-w-0 space-y-1">
											<p class="text-sm font-medium">Schedule</p>
											<CustomComboBox
												bind:open={scheduleComboboxOpen}
												bind:value={scheduleForm.cronExpr}
												data={currentScheduleOptions}
												placeholder="Select or enter a cron expression"
												width="w-full"
												allowCustom={true}
												disallowEmpty={true}
												disabled={scheduleEditLocked || scheduleAction !== ''}
												classes=""
											/>
										</div>
									</div>

									<div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
										<div>
											<p class="text-sm">{scheduleCronDescription || 'Invalid cron expression'}</p>
										</div>
										<CustomCheckbox
											label="Enabled"
											disabled={scheduleEditLocked || scheduleAction !== ''}
											bind:checked={scheduleForm.enabled}
										/>
									</div>

									<div class="flex justify-end gap-2">
										<Button
											size="sm"
											variant="outline"
											disabled={scheduleAction !== ''}
											onclick={() => (scheduleFormOpen = false)}
										>
											Back
										</Button>
										<Button
											size="sm"
											disabled={scheduleEditLocked || scheduleAction !== '' || scheduleCronInvalid}
											onclick={saveSchedule}
										>
											{#if scheduleAction === 'save'}
												<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
											{/if}
											Save Schedule
										</Button>
									</div>
								</div>
							{:else}
								{#if schedulesLoading}
									<div
										class="text-muted-foreground flex h-20 items-center justify-center gap-2 text-sm"
									>
										<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
										<span>Loading schedules</span>
									</div>
								{:else if diskSchedules.length === 0 && !details}
									<div class="text-muted-foreground flex items-center gap-2 text-sm">
										<span class="icon-[material-symbols--event-busy-outline] h-4 w-4"></span>
										<span
											>Self-test capabilities are unavailable and no saved schedule was found for
											this disk.</span
										>
									</div>
								{:else if diskSchedules.length === 0 && supportedScheduleTypes.length === 0}
									<div class="text-muted-foreground flex items-center gap-2 text-sm">
										<span class="icon-[material-symbols--event-busy-outline] h-4 w-4"></span>
										<span>This disk does not support schedulable short or extended tests.</span>
									</div>
								{:else if diskSchedules.length === 0}
									<div
										class="text-muted-foreground flex min-h-52 flex-1 items-center justify-center gap-2 text-sm"
									>
										<span class="icon-[material-symbols--event-busy-outline] h-4 w-4"></span>
										<span>No periodic tests are configured for this disk.</span>
									</div>
								{:else}
									<div class="space-y-3">
										{#each diskSchedules as schedule (schedule.id)}
											<div class="space-y-3 rounded-md border p-4">
												<div class="flex flex-wrap items-center justify-between gap-2">
													<div class="flex items-center gap-2">
														<span class="font-medium">{formatLabel(schedule.testType)} Test</span>
														<Badge
															variant="outline"
															class={scheduleStatusClass(schedule.lastStatus)}
														>
															{formatLabel(schedule.lastStatus)}
														</Badge>
														<Badge
															variant="outline"
															class={schedule.enabled
																? 'text-green-600 dark:text-green-300'
																: 'text-muted-foreground'}
														>
															{schedule.enabled ? 'Enabled' : 'Disabled'}
														</Badge>
													</div>
													<div class="flex items-center gap-1">
														<Button
															size="sm"
															variant="ghost"
															disabled={scheduleStatusIsActive(schedule.lastStatus) ||
																scheduleAction !== ''}
															onclick={() => openEditSchedule(schedule)}
															title={scheduleStatusIsActive(schedule.lastStatus)
																? 'An active schedule cannot be edited'
																: 'Edit schedule'}
														>
															<span class="icon-[mdi--pencil] h-4 w-4"></span>
														</Button>
														<Button
															size="sm"
															variant="ghost"
															disabled={scheduleStatusIsActive(schedule.lastStatus) ||
																scheduleAction !== ''}
															onclick={() => {
																deleteSchedule = schedule;
																deleteConfirmOpen = true;
															}}
															title={scheduleStatusIsActive(schedule.lastStatus)
																? 'An active schedule cannot be deleted'
																: 'Delete schedule'}
														>
															<span class="icon-[mdi--delete-outline] h-4 w-4"></span>
														</Button>
													</div>
												</div>

												<div class="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
													<div>
														<p class="text-muted-foreground text-xs">Schedule (Node Local)</p>
														<p title={schedule.cronExpr}>
															{cronToHuman(schedule.cronExpr) || schedule.cronExpr}
														</p>
													</div>
													<div>
														<p class="text-muted-foreground text-xs">Next Run (Browser Local)</p>
														<p>
															{schedule.enabled
																? formatScheduleTime(schedule.nextRunAt, 'Not scheduled')
																: 'Disabled'}
														</p>
													</div>
													<div>
														<p class="text-muted-foreground text-xs">Last Run (Browser Local)</p>
														<p>{formatScheduleTime(schedule.lastRunAt, 'Never')}</p>
													</div>
													<div>
														<p class="text-muted-foreground text-xs">Estimated Duration</p>
														<p>
															{formatDuration(
																schedule.estimatedMinutes ||
																	durationMinutes(schedule.testType, details?.capabilities)
															)}
														</p>
													</div>
												</div>

												{#if schedule.lastStatus === 'queued' && schedule.queuedAt}
													<p class="text-muted-foreground text-xs">
														Queued {formatScheduleTime(schedule.queuedAt, '')} browser local; it starts
														when the global test slot is available.
													</p>
												{/if}

												{#if scheduleStatusIsActive(schedule.lastStatus)}
													<div class="space-y-2">
														<div class="flex items-center justify-between text-xs">
															<span
																>{schedule.lastStatus === 'starting'
																	? 'Starting scheduled test'
																	: 'Scheduled test progress'}</span
															>
															<span class="text-muted-foreground">
																{schedule.progressKnown && schedule.progressPct >= 0
																	? `${schedule.progressPct}%`
																	: schedule.lastStatus === 'starting'
																		? 'Waiting for device acknowledgement'
																		: 'Progress not reported'}
															</span>
														</div>
														<div class="bg-secondary h-2 w-full overflow-hidden rounded-full">
															{#if schedule.progressKnown && schedule.progressPct >= 0}
																<div
																	class="h-full rounded-full bg-blue-500 transition-[width]"
																	style="width: {Math.min(100, Math.max(0, schedule.progressPct))}%"
																></div>
															{:else}
																<div
																	class="h-full w-full animate-pulse rounded-full bg-blue-500"
																></div>
															{/if}
														</div>
													</div>
												{/if}

												{#if schedule.lastError}
													<div
														class="border-destructive/30 bg-destructive/5 text-destructive rounded-md border p-2 text-xs"
													>
														{schedule.lastError}
													</div>
												{/if}
											</div>
										{/each}
									</div>
								{/if}

								{#if !schedulesLoading && unavailableDiskSchedules.length > 0}
									<details class="border-t pt-4">
										<summary
											class="hover:bg-muted flex cursor-pointer list-none items-center gap-2 rounded-md px-2 py-1.5 text-sm font-medium"
										>
											<span
												class="icon-[material-symbols--warning-outline] h-4 w-4 text-yellow-600 dark:text-yellow-300"
											></span>
											<span>Unavailable disk schedules</span>
											<Badge variant="outline">{unavailableDiskSchedules.length}</Badge>
											<span class="icon-[mdi--chevron-down] text-muted-foreground ml-auto h-4 w-4"
											></span>
										</summary>
										<p class="text-muted-foreground mt-2 px-2 text-xs">
											These schedules reference disks that are not currently present. They can only
											be disabled or deleted.
										</p>

										<div class="mt-3 space-y-2">
											{#each unavailableDiskSchedules as schedule (schedule.id)}
												<div
													class="flex flex-col gap-3 rounded-md border p-3 sm:flex-row sm:items-center sm:justify-between"
												>
													<div class="flex min-w-0 flex-wrap items-center gap-2 text-sm">
														<span class="truncate font-mono font-medium">{schedule.device}</span>
														<Badge variant="outline">{formatLabel(schedule.testType)}</Badge>
														<Badge
															variant="outline"
															class={scheduleStatusClass(schedule.lastStatus)}
														>
															{formatLabel(schedule.lastStatus)}
														</Badge>
														<Badge
															variant="outline"
															class={schedule.enabled
																? 'text-green-600 dark:text-green-300'
																: 'text-muted-foreground'}
														>
															{schedule.enabled ? 'Enabled' : 'Disabled'}
														</Badge>
													</div>
													<div class="flex shrink-0 items-center gap-2">
														<Button
															size="sm"
															variant="outline"
															disabled={!schedule.enabled ||
																scheduleStatusIsActive(schedule.lastStatus) ||
																scheduleAction !== ''}
															onclick={() => disableUnavailableSchedule(schedule)}
															title={scheduleStatusIsActive(schedule.lastStatus)
																? 'An active schedule cannot be disabled'
																: schedule.enabled
																	? 'Disable schedule'
																	: 'Schedule is disabled'}
														>
															{#if scheduleAction === 'disable' && disabledScheduleID === schedule.id}
																<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
															{/if}
															{schedule.enabled ? 'Disable' : 'Disabled'}
														</Button>
														<Button
															size="sm"
															variant="destructive"
															disabled={scheduleStatusIsActive(schedule.lastStatus) ||
																scheduleAction !== ''}
															onclick={() => {
																deleteSchedule = schedule;
																deleteConfirmOpen = true;
															}}
															title={scheduleStatusIsActive(schedule.lastStatus)
																? 'An active schedule cannot be deleted'
																: 'Delete schedule'}
														>
															Delete
														</Button>
													</div>
												</div>
											{/each}
										</div>
									</details>
								{/if}
							{/if}
						</div>
					</div>
				</Tabs.Content>

				<Tabs.Content value="history" class="m-0 min-h-0 overflow-y-auto p-4">
					{#if loading && !details}
						<div class="text-muted-foreground flex h-48 items-center justify-center gap-2">
							<span class="icon-[mdi--loading] h-5 w-5 animate-spin"></span>
							<span>Reading self-test history</span>
						</div>
					{:else if errorMessage && !details}
						<div
							class="border-destructive/40 bg-destructive/10 text-destructive rounded-md border p-4"
						>
							<p>{errorMessage}</p>
						</div>
					{:else if details}
						<div class="flex h-full min-h-0 flex-col gap-4">
							{#if details.capabilities.protocol === 'SCSI' && !details.capabilities.result_log}
								<div class="text-muted-foreground flex items-center gap-2 text-sm">
									<span class="icon-[material-symbols--info-outline] h-4 w-4"></span>
									<span>Self-test result history is unavailable for this device.</span>
								</div>
							{:else if details.status.results.length > 0}
								{#if details.capabilities.result_log && !details.status.checksum_valid}
									<div
										class="border-yellow-500/40 bg-yellow-500/10 rounded-md border p-3 text-sm text-yellow-700 dark:text-yellow-300"
									>
										The device returned a self-test log with an invalid checksum.
									</div>
								{/if}

								{#if activeTab === 'history'}
									<ModalTable
										rows={historyRows}
										columns={historyColumns}
										pageSize={10}
										initialSort={[{ column: 'sequence', dir: 'asc' }]}
										placeholder="No self-test results are available"
									/>
								{/if}
							{:else}
								<div class="text-muted-foreground flex items-center gap-2 text-sm">
									<span class="icon-[material-symbols--info-outline] h-4 w-4"></span>
									<span>No self-test results are available.</span>
								</div>
							{/if}
						</div>
					{/if}
				</Tabs.Content>
			</div>
		</Tabs.Root>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog.Root bind:open={startConfirmOpen}>
	<AlertDialog.Content onInteractOutside={(event) => event.preventDefault()} class="p-5">
		<AlertDialog.Header>
			<AlertDialog.Title>Start {selectedDefinition?.label || 'S.M.A.R.T'} test?</AlertDialog.Title>
			<AlertDialog.Description class="space-y-3">
				<span class="block text-justify">
					The estimated duration is <span class="font-bold">{formatDuration(selectedDuration)}</span
					>. The disk remains available, but the diagnostic uses device resources and may reduce I/O
					performance while it runs. Extended tests can run for several hours. Closing the status
					window does <span class="font-bold">not</span> stop the test.
				</span>
			</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel disabled={action !== ''}>Cancel</AlertDialog.Cancel>
			<AlertDialog.Action
				onclick={startTest}
				disabled={manualStartBlocked || selectedActiveState || action !== ''}
			>
				{#if action === 'start'}
					<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
					Starting
				{:else}
					Start Test
				{/if}
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>

<AlertDialog.Root bind:open={deleteConfirmOpen}>
	<AlertDialog.Content onInteractOutside={(event) => event.preventDefault()} class="p-5">
		<AlertDialog.Header>
			<AlertDialog.Title>Delete periodic self-test schedule?</AlertDialog.Title>
			<AlertDialog.Description>
				{#if deleteScheduleLocked}
					This schedule is starting or running and cannot be deleted until the test finishes.
				{:else}
					The {formatLabel(deleteSchedule?.testType || '')} schedule for {deleteSchedule?.device ||
						disk.device} will be removed. This does not stop or remove completed test results.
				{/if}
			</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel disabled={scheduleAction !== ''} onclick={() => (deleteSchedule = null)}>
				Cancel
			</AlertDialog.Cancel>
			<AlertDialog.Action
				class="bg-destructive text-white hover:bg-destructive/90"
				onclick={confirmDeleteSchedule}
				disabled={deleteScheduleLocked || scheduleAction !== ''}
			>
				{#if scheduleAction === 'delete'}
					<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
					Deleting
				{:else}
					Delete Schedule
				{/if}
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>

<AlertDialog.Root bind:open={abortConfirmOpen}>
	<AlertDialog.Content onInteractOutside={(event) => event.preventDefault()} class="p-5">
		<AlertDialog.Header>
			<AlertDialog.Title>Abort the running S.M.A.R.T test?</AlertDialog.Title>
			<AlertDialog.Description>
				The current diagnostic on {disk.device} will stop and its result may be recorded as aborted.
			</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel disabled={action !== ''}>Cancel</AlertDialog.Cancel>
			<AlertDialog.Action
				class="bg-destructive text-white hover:bg-destructive/90"
				onclick={abortTest}
				disabled={action !== ''}
			>
				{#if action === 'abort'}
					<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
					Aborting
				{:else}
					Abort Test
				{/if}
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
