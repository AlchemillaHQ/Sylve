<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { PeriodicSnapshot } from '$lib/types/zfs/dataset';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { cronToHuman } from '$lib/utils/time';
	import { modifyPeriodicSnapshot } from '$lib/api/zfs/datasets';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		snapshot: PeriodicSnapshot | null;
		dataset: string;
		reload: boolean;
	}

	let { open = $bindable(), snapshot = null, dataset = '', reload = $bindable() }: Props = $props();

	let properties = $state({
		interval: {
			open: false,
			value: snapshot?.cronExpr ? 'cronExpr' : 'minutes',
			data: [
				{ value: 'minutes', label: 'Simple' },
				{ value: 'cronExpr', label: 'Cron Expression' }
			],
			values: {
				cron: snapshot?.cronExpr || '',
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
					value: snapshot?.interval.toString() || '86400'
				}
			}
		},
		retention: {
			open: false,
			value: snapshot?.keepLast || snapshot?.maxAgeDays ? 'simple' : 'gfs',
			data: [
				{ value: 'simple', label: 'Simple' },
				{ value: 'gfs', label: 'GFS' }
			],
			values: {
				simple: {
					keepLast: snapshot?.keepLast || 0,
					maxAgeDays: snapshot?.maxAgeDays || 0
				},
				gfs: {
					keepHourly: snapshot?.keepHourly || 0,
					keepDaily: snapshot?.keepDaily || 0,
					keepWeekly: snapshot?.keepWeekly || 0,
					keepMonthly: snapshot?.keepMonthly || 0,
					keepYearly: snapshot?.keepYearly || 0
				}
			}
		}
	});

	async function save() {
		const response = await modifyPeriodicSnapshot(
			snapshot?.id as number,
			properties.retention.values.simple.keepLast || null,
			properties.retention.values.simple.maxAgeDays || null,
			properties.retention.values.gfs.keepHourly || null,
			properties.retention.values.gfs.keepDaily || null,
			properties.retention.values.gfs.keepWeekly || null,
			properties.retention.values.gfs.keepMonthly || null,
			properties.retention.values.gfs.keepYearly || null
		);

		reload = true;

		if (response.error) {
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

						<span>Retention Policies - </span>
						<span>{dataset}@{snapshot?.prefix}</span>
					</div>
				</Dialog.Title>
				<Dialog.Description></Dialog.Description>
			</Dialog.Header>

			<Dialog.Close
				class="flex h-5 w-5 items-center justify-center rounded-sm opacity-70 transition-opacity hover:opacity-100"
				onclick={() => {
					open = false;
				}}
			>
				<span class="icon-[material-symbols--close-rounded] h-5 w-5"></span>
			</Dialog.Close>
		</div>

		<div class="flex flex-row gap-2">
			<CustomComboBox
				bind:open={properties.interval.open}
				label="Interval"
				bind:value={properties.interval.value}
				data={properties.interval.data}
				classes="w-full space-y-1"
				placeholder="Select an interval"
				width="w-full"
			/>

			{#if properties.interval.value === 'minutes'}
				<CustomComboBox
					bind:open={properties.interval.values.interval.open}
					label="Interval"
					bind:value={properties.interval.values.interval.value}
					data={properties.interval.values.interval.data}
					classes="w-full space-y-1"
					placeholder="Select an interval"
					width="w-full"
				/>
			{:else if properties.interval.value === 'cronExpr'}
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
			{/if}

			<CustomComboBox
				bind:open={properties.retention.open}
				label="Retention"
				bind:value={properties.retention.value}
				data={properties.retention.data}
				classes="w-full space-y-1"
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
