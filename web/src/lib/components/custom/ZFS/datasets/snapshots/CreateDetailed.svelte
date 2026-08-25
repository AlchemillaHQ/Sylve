<script lang="ts">
	import { createPeriodicSnapshot, createSnapshot, getDatasets } from '$lib/api/zfs/datasets';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { BasicSettings } from '$lib/types/system/settings';
	import type { APIResponse } from '$lib/types/common';
	import { GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';
	import { getAPIErrorMessages, handleAPIError } from '$lib/utils/http';
	import { cronToHuman } from '$lib/utils/time';
	import { getDashedDate } from '$lib/utils/time.svelte';
	import { deepEqual } from 'fast-equals';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import { generateSimpleSelectOptions } from '$lib/utils/input';

	interface Props {
		open: boolean;
		basicSettings: BasicSettings;
		reload?: boolean;
		prefill?: { pool: string; dataset: string };
	}

	let { open = $bindable(), reload = $bindable(), basicSettings, prefill }: Props = $props();
	const simpleRetentionDefaults = { keepLast: '24', maxAgeDays: '0' };
	const gfsRetentionDefaults = {
		keepLast: '20',
		keepHourly: '24',
		keepDaily: '7',
		keepWeekly: '4',
		keepMonthly: '12',
		keepYearly: '3'
	};

	let datasets = resource(
		() => 'zfs-fs-vol-datasets',
		async () => {
			const fs = await getDatasets(GZFSDatasetTypeSchema.enum.FILESYSTEM);
			const vol = await getDatasets(GZFSDatasetTypeSchema.enum.VOLUME);
			return [...fs, ...vol];
		},
		{
			initialValue: []
		}
	);

	function createInitialProperties() {
		return {
			name: `manual-${getDashedDate()}`,
			pool: {
				open: false,
				value: prefill?.pool || (basicSettings.pools.length === 1 ? basicSettings.pools[0] : ''),
				data: generateSimpleSelectOptions(basicSettings.pools)
			},
			datasets: {
				open: false,
				value: prefill?.dataset || '',
				data: [] as { label: string; value: string }[]
			},
			interval: {
				type: 'none' as 'none' | 'minutes' | 'cronExpr',
				open: false,
				value: 'none',
				data: [
					{ value: 'none', label: 'None' },
					{ value: 'minutes', label: 'Simple' },
					{ value: 'cronExpr', label: 'Cron Expression' }
				],
				values: {
					cron: '',
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
						value: ''
					}
				}
			},
			retention: {
				open: false,
				value: 'none',
				data: [
					{ value: 'none', label: 'None' },
					{ value: 'simple', label: 'Simple' },
					{ value: 'gfs', label: 'GFS' }
				],
				simple: {
					...simpleRetentionDefaults
				},
				gfs: {
					...gfsRetentionDefaults
				}
			},
			recursive: false
		};
	}

	let properties = $state(createInitialProperties());
	let cronDescription = $derived(
		properties.interval.values.cron.trim() ? cronToHuman(properties.interval.values.cron) : ''
	);

	watch([() => properties.pool.value, () => datasets.current], ([poolValue]) => {
		if (poolValue) {
			const sets = datasets.current
				.filter((dataset) => dataset.pool === poolValue)
				.map((dataset) => ({
					label: dataset.name,
					value: dataset.name
				}));

			if (deepEqual(sets, properties.datasets.data) === false) {
				properties.datasets.data = sets;
			}
		}
	});

	function showSnapshotCreateError(
		response: APIResponse,
		datasetName: string,
		snapshotName: string
	) {
		if (
			getAPIErrorMessages(response).some((message) => message.includes('dataset already exists'))
		) {
			toast.error(`Snapshot ${datasetName}@${snapshotName} already exists`, {
				position: 'bottom-center'
			});
			return;
		}

		handleAPIError(response);
		toast.error('Failed to create snapshot', {
			position: 'bottom-center'
		});
	}

	async function create() {
		if (properties.name.trim() === '') {
			toast.error('Name/prefix required for snapshot(s)', {
				position: 'bottom-center'
			});
			return;
		}

		if (properties.pool.value === '') {
			toast.error('No pool selected', {
				position: 'bottom-center'
			});
			return;
		}

		if (properties.datasets.value === '') {
			toast.error('No dataset selected', {
				position: 'bottom-center'
			});
			return;
		}

		const dataset = datasets.current.find((dataset) => dataset.name === properties.datasets.value);

		if (dataset) {
			const intervalType = properties.interval.value;
			const retentionType = properties.retention.value;
			let response: APIResponse | null = null;
			let minutes: number = 0;
			let cron: string = '';

			if (intervalType === 'none' || intervalType === '') {
				response = await createSnapshot(dataset, properties.name, properties.recursive);

				if (response.status !== 'success') {
					showSnapshotCreateError(response, dataset.name, properties.name);
					return;
				}

				toast.success(`Snapshot ${dataset.name}@${properties.name} created`, {
					position: 'bottom-center'
				});

				reload = true;
				properties = createInitialProperties();
				open = false;
				return;
			} else if (intervalType === 'minutes') {
				minutes = parseInt(properties.interval.values.interval.value) || 0;
			} else if (intervalType === 'cronExpr') {
				cron = properties.interval.values.cron;
			}

			if (retentionType === 'simple') {
				const values = properties.retention.simple;
				if (![values.keepLast, values.maxAgeDays].some((value) => Number(value) > 0)) {
					toast.error('At least one retention value must be greater than zero', {
						position: 'bottom-center'
					});
					return;
				}
			} else if (retentionType === 'gfs') {
				const values = properties.retention.gfs;
				if (!Object.values(values).some((value) => Number(value) > 0)) {
					toast.error('At least one retention value must be greater than zero', {
						position: 'bottom-center'
					});
					return;
				}
			}

			if (retentionType !== 'none') {
				if (retentionType === 'simple') {
					response = await createPeriodicSnapshot(
						dataset,
						properties.name,
						properties.recursive,
						minutes,
						cron,
						'simple',
						Number(properties.retention.simple.keepLast),
						Number(properties.retention.simple.maxAgeDays)
					);
				} else if (retentionType === 'gfs') {
					response = await createPeriodicSnapshot(
						dataset,
						properties.name,
						properties.recursive,
						minutes,
						cron,
						'gfs',
						Number(properties.retention.gfs.keepLast),
						null,
						Number(properties.retention.gfs.keepHourly),
						Number(properties.retention.gfs.keepDaily),
						Number(properties.retention.gfs.keepWeekly),
						Number(properties.retention.gfs.keepMonthly),
						Number(properties.retention.gfs.keepYearly)
					);
				}
			} else {
				response = await createPeriodicSnapshot(
					dataset,
					properties.name,
					properties.recursive,
					minutes,
					cron,
					'none',
					null,
					null,
					null,
					null,
					null,
					null
				);
			}

			if (!response || response.status !== 'success') {
				if (response) {
					showSnapshotCreateError(response, dataset.name, properties.name);
				} else {
					toast.error('Failed to create snapshot', {
						position: 'bottom-center'
					});
				}
				return;
			}

			reload = true;
			toast.success(`Snapshot ${dataset.name}@${properties.name} created`, {
				position: 'bottom-center'
			});

			properties = createInitialProperties();
			open = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="p-5"
		showCloseButton={true}
		showResetButton={true}
		onReset={() => {
			properties = createInitialProperties();
		}}
		onClose={() => {
			properties = createInitialProperties();
			open = false;
		}}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[carbon--ibm-cloud-vpc-block-storage-snapshots]"
					size="h-5 w-5"
					gap="gap-2"
					title="Create Snapshot"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<CustomValueInput
			label={`${'Name'} | ${'Prefix'}`}
			placeholder="after-upgrade"
			bind:value={properties.name}
			classes="flex-1 space-y-1"
		/>

		<div class="flex min-w-0 gap-4 overflow-hidden">
			<CustomComboBox
				bind:open={properties.pool.open}
				label="Pool"
				bind:value={properties.pool.value}
				data={basicSettings.pools.map((name) => ({
					label: name,
					value: name
				}))}
				classes="flex-1 w-1/2 space-y-1 min-w-0"
				placeholder="Select a pool"
				width="w-full"
			></CustomComboBox>

			<CustomComboBox
				bind:open={properties.datasets.open}
				label="Dataset"
				bind:value={properties.datasets.value}
				data={properties.datasets.data}
				classes="flex-1 w-1/2 space-y-1 min-w-0 max-w-full"
				placeholder="Select a dataset"
				width="w-full"
			></CustomComboBox>
		</div>

		<div class="w-full space-y-4">
			<div class="flex flex-col items-center gap-4">
				<div class="flex w-full flex-col gap-2">
					<CustomComboBox
						bind:open={properties.interval.open}
						label="Interval Type"
						bind:value={properties.interval.value}
						data={properties.interval.data}
						classes="w-full space-y-1"
						placeholder="Select an interval"
						width="w-full"
					/>

					{#if properties.interval.value === 'cronExpr'}
						<CustomValueInput
							label="Cron Expression"
							hint={cronDescription}
							placeholder="0 0 * * *"
							bind:value={properties.interval.values.cron}
							classes="w-full space-y-1"
						/>
					{:else if properties.interval.value === 'minutes'}
						<CustomComboBox
							bind:open={properties.interval.values.interval.open}
							label="Interval"
							bind:value={properties.interval.values.interval.value}
							data={properties.interval.values.interval.data}
							classes="w-full space-y-1"
							placeholder="Select an interval"
							width="w-full"
						/>
					{/if}
				</div>

				{#if properties.interval.value !== 'none' && properties.interval.value !== ''}
					<CustomComboBox
						bind:open={properties.retention.open}
						label="Retention"
						bind:value={properties.retention.value}
						data={properties.retention.data}
						classes="w-full space-y-1"
						placeholder="Select a retention policy"
						width="w-full"
					/>
				{/if}
			</div>

			{#if properties.retention.value === 'simple'}
				<div class="flex flex-row items-center gap-4">
					<CustomValueInput
						label="Keep Last"
						type="number"
						placeholder="0"
						bind:value={properties.retention.simple.keepLast}
						classes="w-full space-y-1"
					/>
					<CustomValueInput
						label="Max Age (Days)"
						type="number"
						placeholder="0"
						bind:value={properties.retention.simple.maxAgeDays}
						classes="w-full space-y-1"
					/>
				</div>
			{:else if properties.retention.value === 'gfs'}
				<div class="grid grid-cols-3 gap-4">
					<CustomValueInput
						label="Keep Last"
						type="number"
						placeholder="0"
						bind:value={properties.retention.gfs.keepLast}
						classes="w-full space-y-1"
					/>
					<CustomValueInput
						label="Keep Hourly"
						type="number"
						placeholder="0"
						bind:value={properties.retention.gfs.keepHourly}
						classes="w-full space-y-1"
					/>
					<CustomValueInput
						label="Keep Daily"
						type="number"
						placeholder="0"
						bind:value={properties.retention.gfs.keepDaily}
						classes="w-full space-y-1"
					/>
					<CustomValueInput
						label="Keep Weekly"
						type="number"
						placeholder="0"
						bind:value={properties.retention.gfs.keepWeekly}
						classes="w-full space-y-1"
					/>
					<CustomValueInput
						label="Keep Monthly"
						type="number"
						placeholder="0"
						bind:value={properties.retention.gfs.keepMonthly}
						classes="w-full space-y-1"
					/>
					<CustomValueInput
						label="Keep Yearly"
						type="number"
						placeholder="0"
						bind:value={properties.retention.gfs.keepYearly}
						classes="w-full space-y-1"
					/>
				</div>
			{/if}
		</div>

		<CustomCheckbox
			label="Recursive"
			bind:checked={properties.recursive}
			classes="flex items-center gap-2"
		></CustomCheckbox>

		<Dialog.Footer>
			<Button
				size="sm"
				class="w-full lg:w-28"
				onclick={() => {
					create();
				}}>Create</Button
			>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
