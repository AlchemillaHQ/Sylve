<script lang="ts">
	import { createPeriodicSnapshot, createSnapshot } from '$lib/api/zfs/datasets';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { APIResponse } from '$lib/types/common';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { handleAPIError } from '$lib/utils/http';
	import { cronToHuman } from '$lib/utils/time';
	import Icon from '@iconify/svelte';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		pools: Zpool[];
		datasets: Dataset[];
		reload?: boolean;
	}

	let { open = $bindable(), pools, datasets, reload = $bindable() }: Props = $props();

	let options = {
		name: '',
		pool: {
			open: false,
			value: '',
			data: pools.map((pool) => ({
				label: pool.name,
				value: pool.name
			}))
		},
		datasets: {
			open: false,
			value: '',
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
				keepLast: '0',
				maxAgeDays: '0'
			},
			gfs: {
				keepLast: '0',
				keepHourly: '0',
				keepDaily: '0',
				keepWeekly: '0',
				keepMonthly: '0',
				keepYearly: '0'
			}
		},
		recursive: false
	};

	let properties = $state(options);

	$effect(() => {
		if (properties.pool.value) {
			const sets = datasets
				.filter((dataset) => dataset.name.startsWith(properties.pool.value))
				.map((dataset) => ({
					label: dataset.name,
					value: dataset.name
				}));

			if (JSON.stringify(sets) !== JSON.stringify(properties.datasets.data)) {
				properties.datasets.data = sets;
			}
		}
	});

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

		const dataset = datasets.find((dataset) => dataset.name === properties.datasets.value);
		const pool = pools.find((pool) => pool.name === properties.pool.value);

		if (dataset) {
			const intervalType = properties.interval.value;
			const retentionType = properties.retention.value;
			let response: APIResponse | null = null;
			let minutes: number = 0;
			let cron: string = '';

			if (intervalType === 'none') {
				response = await createSnapshot(dataset, properties.name, properties.recursive);
			} else if (intervalType === 'minutes') {
				minutes = parseInt(properties.interval.values.interval.value) || 0;
			} else if (intervalType === 'cronExpr') {
				cron = properties.interval.values.cron;
			}

			if (retentionType !== 'none') {
				if (retentionType === 'simple') {
					response = await createPeriodicSnapshot(
						dataset,
						properties.name,
						properties.recursive,
						minutes,
						cron,
						parseInt(properties.retention.simple.keepLast) || null,
						parseInt(properties.retention.simple.maxAgeDays) || null
					);
				} else if (retentionType === 'gfs') {
					response = await createPeriodicSnapshot(
						dataset,
						properties.name,
						properties.recursive,
						minutes,
						cron,
						null,
						null,
						parseInt(properties.retention.gfs.keepHourly) || null,
						parseInt(properties.retention.gfs.keepDaily) || null,
						parseInt(properties.retention.gfs.keepWeekly) || null,
						parseInt(properties.retention.gfs.keepMonthly) || null,
						parseInt(properties.retention.gfs.keepYearly) || null
					);
				}
			} else {
                response = await createPeriodicSnapshot(
                    dataset,
                    properties.name,
                    properties.recursive,
                    minutes,
                    cron,
                    null,
                    null,
                    null,
                    null,
                    null,
                    null
                );
            }

			reload = true;

            console.log(response);

			if (response?.error) {
				handleAPIError(response);
				toast.error('Failed to create snapshot', {
					position: 'bottom-center'
				});
				return;
			} else {
				toast.success(`Snapshot ${pool?.name}@${properties.name} created`, {
					position: 'bottom-center'
				});

				properties = options;
				open = false;
			}
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-3/4 p-5">
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex justify-between">
				<div class="flex items-center">
					<Icon icon="carbon:ibm-cloud-vpc-block-storage-snapshots" class="mr-2 h-6 w-6" />
					<span>Create Snapshot</span>
				</div>
				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Reset'}
						onclick={() => {
							properties = options;
						}}
					>
						<Icon icon="radix-icons:reset" class="pointer-events-none h-4 w-4" />
						<span class="sr-only">{'Reset'}</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							properties = options;
							open = false;
						}}
					>
						<Icon icon="material-symbols:close-rounded" class="pointer-events-none h-4 w-4" />
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<CustomValueInput
			label={`${'Name'} | ${'Prefix'}`}
			placeholder="after-upgrade"
			bind:value={properties.name}
			classes="flex-1 space-y-1"
		/>

		<div class="flex gap-4">
			<CustomComboBox
				bind:open={properties.pool.open}
				label="Pool"
				bind:value={properties.pool.value}
				data={properties.pool.data}
				classes="flex-1 space-y-1"
				placeholder="Select a pool"
				width="w-full"
			></CustomComboBox>

			<CustomComboBox
				bind:open={properties.datasets.open}
				label="Dataset"
				bind:value={properties.datasets.value}
				data={properties.datasets.data}
				classes="flex-1 space-y-1"
				placeholder="Select a dataset"
				width="w-full"
			></CustomComboBox>
		</div>

		<div class="w-full space-y-4">
			<div class="flex flex-col items-center gap-4">
				<div class="flex w-full flex-col gap-2">
					<CustomComboBox
						bind:open={properties.interval.open}
						label="Interval"
						bind:value={properties.interval.value}
						data={properties.interval.data}
						classes="w-full space-y-1"
						placeholder="Select an interval"
						width="w-full"
					/>

					{#if properties.interval.value === 'cronExpr'}
						<CustomValueInput
							label={`
                    <span class="text-sm font-medium text-gray-200">
                        Cron Expression${
													cronToHuman(properties.interval.values.cron)
														? `&nbsp;<span class="text-green-300 font-semibold">(${cronToHuman(properties.interval.values.cron)})</span>`
														: ''
												}
                    </span>
                    `}
							labelHTML={true}
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

				{#if properties.interval.value !== 'none'}
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
						placeholder="0"
						bind:value={properties.retention.simple.keepLast}
						classes="w-full space-y-1"
					/>
					<CustomValueInput
						label="Max Age (Days)"
						placeholder="0"
						bind:value={properties.retention.simple.maxAgeDays}
						classes="w-full space-y-1"
					/>
				</div>
			{:else if properties.retention.value === 'gfs'}
				<div class="grid grid-cols-3 gap-4">
					<CustomValueInput
						label="Keep Last"
						placeholder="0"
						bind:value={properties.retention.gfs.keepLast}
						classes="w-full space-y-1"
					/>
					<CustomValueInput
						label="Keep Hourly"
						placeholder="0"
						bind:value={properties.retention.gfs.keepHourly}
						classes="w-full space-y-1"
					/>
					<CustomValueInput
						label="Keep Daily"
						placeholder="0"
						bind:value={properties.retention.gfs.keepDaily}
						classes="w-full space-y-1"
					/>
					<CustomValueInput
						label="Keep Weekly"
						placeholder="0"
						bind:value={properties.retention.gfs.keepWeekly}
						classes="w-full space-y-1"
					/>
					<CustomValueInput
						label="Keep Monthly"
						placeholder="0"
						bind:value={properties.retention.gfs.keepMonthly}
						classes="w-full space-y-1"
					/>
					<CustomValueInput
						label="Keep Yearly"
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
