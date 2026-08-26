<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { PeriodicSnapshot } from '$lib/types/zfs/dataset';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { cronToHuman } from '$lib/utils/time';
	import { modifyPeriodicSnapshot } from '$lib/api/zfs/datasets';
	import { handleAPIError } from '$lib/utils/http';
	import { untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		snapshot: PeriodicSnapshot | null;
		dataset: string;
		reload: boolean;
	}

	let { open = $bindable(), snapshot = null, dataset = '', reload = $bindable() }: Props = $props();
	const simpleRetentionDefaults = { keepLast: 24, maxAgeDays: 0 };
	const gfsRetentionDefaults = {
		keepLast: 20,
		keepHourly: 24,
		keepDaily: 7,
		keepWeekly: 4,
		keepMonthly: 12,
		keepYearly: 3
	};

	function createProperties(source: PeriodicSnapshot | null) {
		const hasGFS = Boolean(
			source?.keepHourly ||
			source?.keepDaily ||
			source?.keepWeekly ||
			source?.keepMonthly ||
			source?.keepYearly
		);
		const hasSimple = Boolean(source?.keepLast || source?.maxAgeDays);
		const retentionType = hasGFS ? 'gfs' : hasSimple ? 'simple' : 'none';

		return {
			interval: {
				open: false,
				value: source?.cronExpr ? 'cronExpr' : 'minutes',
				data: [
					{ value: 'minutes', label: 'Simple' },
					{ value: 'cronExpr', label: 'Cron Expression' }
				],
				values: {
					cron: source?.cronExpr || '',
					interval: {
						open: false,
						data: [
							{ value: '60', label: 'Every Minute' },
							{ value: '3600', label: 'Every Hour' },
							{ value: '86400', label: 'Every Day' },
							{ value: '604800', label: 'Every Week' },
							{ value: '2419200', label: 'Every Month' },
							{ value: '29030400', label: 'Every Year' }
						],
						value: source?.interval.toString() || '86400'
					}
				}
			},
			retention: {
				open: false,
				value: retentionType,
				data: [
					{ value: 'none', label: 'None' },
					{ value: 'simple', label: 'Simple' },
					{ value: 'gfs', label: 'GFS' }
				],
				values: {
					simple: {
						keepLast:
							retentionType === 'simple' ? source?.keepLast || 0 : simpleRetentionDefaults.keepLast,
						maxAgeDays:
							retentionType === 'simple'
								? source?.maxAgeDays || 0
								: simpleRetentionDefaults.maxAgeDays
					},
					gfs: {
						keepLast:
							retentionType === 'gfs' ? source?.keepLast || 0 : gfsRetentionDefaults.keepLast,
						keepHourly:
							retentionType === 'gfs' ? source?.keepHourly || 0 : gfsRetentionDefaults.keepHourly,
						keepDaily:
							retentionType === 'gfs' ? source?.keepDaily || 0 : gfsRetentionDefaults.keepDaily,
						keepWeekly:
							retentionType === 'gfs' ? source?.keepWeekly || 0 : gfsRetentionDefaults.keepWeekly,
						keepMonthly:
							retentionType === 'gfs' ? source?.keepMonthly || 0 : gfsRetentionDefaults.keepMonthly,
						keepYearly:
							retentionType === 'gfs' ? source?.keepYearly || 0 : gfsRetentionDefaults.keepYearly
					}
				}
			}
		};
	}

	const snapshotAtOpen = untrack(() => snapshot);
	let properties = $state(createProperties(snapshotAtOpen));
	let cronDescription = $derived(
		properties.interval.values.cron.trim() ? cronToHuman(properties.interval.values.cron) : ''
	);

	async function save() {
		if (!snapshotAtOpen) {
			toast.error('Snapshot job is unavailable', { position: 'bottom-center' });
			return;
		}

		const simpleSchedule = properties.interval.value === 'minutes';
		const retentionType = properties.retention.value as 'none' | 'simple' | 'gfs';
		if (retentionType === 'simple') {
			const values = properties.retention.values.simple;
			if (![values.keepLast, values.maxAgeDays].some((value) => Number(value) > 0)) {
				toast.error('At least one retention value must be greater than zero', {
					position: 'bottom-center'
				});
				return;
			}
		} else if (retentionType === 'gfs') {
			if (!Object.values(properties.retention.values.gfs).some((value) => Number(value) > 0)) {
				toast.error('At least one retention value must be greater than zero', {
					position: 'bottom-center'
				});
				return;
			}
		}

		const response = await modifyPeriodicSnapshot(
			snapshotAtOpen.id,
			simpleSchedule ? Number(properties.interval.values.interval.value) : 0,
			simpleSchedule ? '' : properties.interval.values.cron.trim(),
			retentionType,
			retentionType === 'simple'
				? Number(properties.retention.values.simple.keepLast)
				: retentionType === 'gfs'
					? Number(properties.retention.values.gfs.keepLast)
					: 0,
			retentionType === 'simple'
				? Number(properties.retention.values.simple.maxAgeDays)
				: retentionType === 'none'
					? 0
					: null,
			retentionType === 'gfs' ? Number(properties.retention.values.gfs.keepHourly) : 0,
			retentionType === 'gfs' ? Number(properties.retention.values.gfs.keepDaily) : 0,
			retentionType === 'gfs' ? Number(properties.retention.values.gfs.keepWeekly) : 0,
			retentionType === 'gfs' ? Number(properties.retention.values.gfs.keepMonthly) : 0,
			retentionType === 'gfs' ? Number(properties.retention.values.gfs.keepYearly) : 0
		);

		reload = true;

		if (response.status !== 'success') {
			handleAPIError(response);
			toast.error('Error modifying retention policy', {
				position: 'bottom-center'
			});

			return;
		} else {
			toast.success('Retention policy modified', {
				position: 'bottom-center'
			});

			open = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		onInteractOutside={(e) => e.preventDefault()}
		onEscapeKeydown={(e) => e.preventDefault()}
		class="w-1/2"
	>
		<div class="flex items-center justify-between">
			<Dialog.Header class="p-0">
				<Dialog.Title>
					<div class="flex flex-row gap-2">
						<span class="icon-[lucide--timer-reset] h-5 w-5"></span>
						<span>Retention Policies</span>
					</div>
				</Dialog.Title>
				<Dialog.Description>
					{dataset}@{snapshotAtOpen?.prefix}
				</Dialog.Description>
			</Dialog.Header>
		</div>

		<div class="flex min-w-0 flex-row items-start gap-2">
			<CustomComboBox
				bind:open={properties.interval.open}
				label="Interval Type"
				bind:value={properties.interval.value}
				data={properties.interval.data}
				classes="min-w-0 flex-1 space-y-1"
				placeholder="Select an interval"
				width="w-full"
			/>

			{#if properties.interval.value === 'minutes'}
				<CustomComboBox
					bind:open={properties.interval.values.interval.open}
					label="Interval"
					bind:value={properties.interval.values.interval.value}
					data={properties.interval.values.interval.data}
					classes="min-w-0 flex-1 space-y-1"
					placeholder="Select an interval"
					width="w-full"
				/>
			{:else if properties.interval.value === 'cronExpr'}
				<CustomValueInput
					label="Cron Expression"
					hint={cronDescription}
					placeholder="0 0 * * *"
					bind:value={properties.interval.values.cron}
					classes="min-w-0 flex-1 space-y-1"
				/>
			{/if}

			<CustomComboBox
				bind:open={properties.retention.open}
				label="Retention"
				bind:value={properties.retention.value}
				data={properties.retention.data}
				classes="min-w-0 flex-1 space-y-1"
				placeholder="Select a retention policy"
				width="w-full"
			/>
		</div>

		{#if properties.retention.value === 'simple'}
			<div class="flex w-full flex-row gap-2">
				<CustomValueInput
					label="Keep Last"
					type="number"
					placeholder="e.g. 10"
					bind:value={properties.retention.values.simple.keepLast}
					classes="w-full space-y-1"
				/>
				<CustomValueInput
					label="Max Age (Days)"
					type="number"
					placeholder="e.g. 30"
					bind:value={properties.retention.values.simple.maxAgeDays}
					classes="w-full space-y-1"
				/>
			</div>
		{:else if properties.retention.value === 'gfs'}
			<div class="flex w-full flex-row gap-2">
				<CustomValueInput
					label="Keep Last"
					type="number"
					placeholder="e.g. 20"
					bind:value={properties.retention.values.gfs.keepLast}
					classes="w-full space-y-1"
				/>
				<CustomValueInput
					label="Keep Hourly"
					type="number"
					placeholder="e.g. 24"
					bind:value={properties.retention.values.gfs.keepHourly}
					classes="w-full space-y-1"
				/>
				<CustomValueInput
					label="Keep Daily"
					type="number"
					placeholder="e.g. 7"
					bind:value={properties.retention.values.gfs.keepDaily}
					classes="w-full space-y-1"
				/>
			</div>
			<div class="flex w-full flex-row gap-2">
				<CustomValueInput
					label="Keep Weekly"
					type="number"
					placeholder="e.g. 4"
					bind:value={properties.retention.values.gfs.keepWeekly}
					classes="w-full space-y-1"
				/>
				<CustomValueInput
					label="Keep Monthly"
					type="number"
					placeholder="e.g. 12"
					bind:value={properties.retention.values.gfs.keepMonthly}
					classes="w-full space-y-1"
				/>
				<CustomValueInput
					label="Keep Yearly"
					type="number"
					placeholder="e.g. 5"
					bind:value={properties.retention.values.gfs.keepYearly}
					classes="w-full space-y-1"
				/>
			</div>
		{/if}

		<Dialog.Footer>
			<Button
				size="sm"
				class="w-full lg:w-28"
				onclick={() => {
					save();
				}}>Save</Button
			>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
