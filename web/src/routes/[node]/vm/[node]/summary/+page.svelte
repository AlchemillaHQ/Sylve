<script lang="ts">
	import * as AlertDialogRaw from '$lib/components/ui/alert-dialog/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { goto } from '$app/navigation';
	import * as Card from '$lib/components/ui/card/index.js';
	import {
		actionVm,
		deleteVM,
		getQGAInfo,
		getStats,
		getVmById,
		getVMDomain,
		updateDescription
	} from '$lib/api/vm/vm';
	import LoadingDialog from '$lib/components/custom/Dialog/Loading.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import {
		VMStatSchema,
		type QGAInfo,
		type VM,
		type VMDomain,
		type VMStat
	} from '$lib/types/vm/vm';
	import { getObjectSchemaDefaults, sleep } from '$lib/utils';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { floatToNDecimals } from '$lib/utils/numbers';
	import { dateToAgo } from '$lib/utils/time';
	import humanFormat from 'human-format';
	import { toast } from 'svelte-sonner';
	import { storage } from '$lib';
	import { resource, useInterval, Debounced, IsDocumentVisible, watch } from 'runed';
	import type { APIResponse, GFSStep } from '$lib/types/common';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import LineBrush from '$lib/components/custom/Charts/LineBrush/Single.svelte';
	import { getVMIconByGaId } from '$lib/utils/vm/vm';
	import * as Table from '$lib/components/ui/table/index.js';

	interface Data {
		rid: number;
		vm: VM;
		domain: VMDomain;
		stats: VMStat[];
		gaInfo: QGAInfo | APIResponse;
	}

	let { data }: { data: Data } = $props();
	let gfsStep = $state<GFSStep>('hourly');

	// svelte-ignore state_referenced_locally
	const vm = resource(
		() => 'vm-' + data.vm.rid,
		async (key) => {
			const result = await getVmById(Number(data.vm.rid), 'rid');
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.vm }
	);

	// svelte-ignore state_referenced_locally
	const domain = resource(
		() => `vm-domain-${data.vm.rid}`,
		async (key) => {
			const result = await getVMDomain(data.vm.rid);
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.domain }
	);

	// svelte-ignore state_referenced_locally
	const stats = resource(
		[() => gfsStep],
		async ([gfsStep]) => {
			const result = await getStats(Number(data.vm.id), gfsStep);
			const key = `vm-stats-${data.vm.id}`;
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.stats }
	);

	// svelte-ignore state_referenced_locally
	const gaInfo = resource(
		() => `vm-qga-${data.vm.rid}`,
		async (key) => {
			const result = await getQGAInfo(data.vm.rid);
			if (isAPIResponse(result)) {
				if (!isAPIResponse(data.gaInfo)) return data.gaInfo;
				return null;
			}

			updateCache(key, result);
			return result;
		},
		{ initialValue: isAPIResponse(data.gaInfo) ? null : data.gaInfo }
	);

	const visible = new IsDocumentVisible();

	useInterval(() => 1000, {
		callback: () => {
			if (visible.current) {
				domain.refetch();
			}
		}
	});

	useInterval(() => 3000, {
		callback: () => {
			if (visible.current) {
				stats.refetch();
			}
		}
	});

	watch(
		() => domain.current,
		(currentDomain, prevDomain) => {
			if (prevDomain?.status !== currentDomain.status) {
				vm.refetch();
			}
		}
	);

	watch(
		() => storage.idle,
		(idle) => {
			if (!idle) {
				vm.refetch();
				domain.refetch();
				stats.refetch();
			}
		}
	);

	let recentStat = $derived(
		stats.current[stats.current.length - 1] || getObjectSchemaDefaults(VMStatSchema)
	);

	let vmDescription = $state(vm.current.description || '');
	let debouncedDesc = new Debounced(() => vmDescription, 500);
	let isDescInitialized = false;

	watch(
		() => debouncedDesc.current,
		(curr, prev) => {
			if (!isDescInitialized) {
				isDescInitialized = true;
				return;
			}

			if (curr !== undefined && prev !== undefined) {
				if (curr !== prev) {
					updateDescription(vm.current.rid, curr);
				}
			}
		}
	);

	let modalState = $state({
		isDeleteOpen: false,
		deleteMACs: true,
		deleteRAWDisks: false,
		deleteVolumes: false,
		title: '',
		loading: {
			open: false,
			title: '',
			description: '',
			iconColor: ''
		}
	});

	async function handleDelete() {
		modalState.isDeleteOpen = false;
		modalState.loading.open = true;
		modalState.loading.title = 'Deleting Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is being deleted`;

		await sleep(1000);
		const result = await deleteVM(
			vm.current.rid,
			modalState.deleteMACs,
			modalState.deleteRAWDisks,
			modalState.deleteVolumes
		);
		modalState.loading.open = false;
		reload.leftPanel = true;

		if (result.status === 'error') {
			toast.error('Error deleting VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			goto(`/${storage.hostname}/summary`);
			toast.success('VM deleted', {
				duration: 5000,
				position: 'bottom-center'
			});
		}
	}

	async function handleStart() {
		modalState.loading.open = true;
		modalState.loading.title = 'Starting Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is being started.`;
		modalState.loading.iconColor = 'text-green-500';

		const result = await actionVm(vm.current.rid, 'start');

		reload.leftPanel = true;

		if (result.status === 'error') {
			modalState.loading.open = false;
			toast.error('Error starting VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			await sleep(1000);
			modalState.loading.open = false;
			toast.success('VM started', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		gaInfo.refetch();
	}

	async function handleStop() {
		modalState.loading.open = true;
		modalState.loading.title = 'Stopping Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is being stopped`;
		modalState.loading.iconColor = 'text-red-500';

		const result = await actionVm(vm.current.rid, 'stop');

		reload.leftPanel = true;

		if (result.status === 'error') {
			modalState.loading.open = false;
			toast.error('Error stopping VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			await sleep(1000);
			modalState.loading.open = false;
			toast.success('VM stopped', {
				duration: 5000,
				position: 'bottom-center'
			});
		}
	}

	async function handleShutdown() {
		modalState.loading.open = true;
		modalState.loading.title = 'Shutting Down Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is being shut down`;
		modalState.loading.iconColor = 'text-yellow-500';

		const result = await actionVm(vm.current.rid, 'shutdown');
		reload.leftPanel = true;

		if (result.status === 'error') {
			modalState.loading.open = false;
			toast.error('Error shutting down VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			await sleep(1000);
			modalState.loading.open = false;
			toast.success('VM shut down', {
				duration: 5000,
				position: 'bottom-center'
			});
		}
	}

	async function handleReboot() {
		modalState.loading.open = true;
		modalState.loading.title = 'Rebooting Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is being rebooted`;
		modalState.loading.iconColor = 'text-blue-500';

		const result = await actionVm(vm.current.rid, 'reboot');
		reload.leftPanel = true;

		if (result.status === 'error') {
			modalState.loading.open = false;
			toast.error('Error rebooting VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			await sleep(1000);
			modalState.loading.open = false;
			toast.success('VM rebooted', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		gaInfo.refetch();
	}

	let udTime = $derived.by(() => {
		if (domain.current.status === 'Running') {
			if (vm.current.startedAt) {
				return `Started ${dateToAgo(vm.current.startedAt)}`;
			}
		} else if (domain.current.status === 'Stopped' || domain.current.status === 'Shutoff') {
			if (vm.current.stoppedAt) {
				return `Stopped ${dateToAgo(vm.current.stoppedAt)}`;
			}
		}
		return '';
	});

	let activeGaView = $state('os');
</script>

{#snippet button(type: string)}
	{#if type === 'start' && domain.current.id == -1 && domain.current.status !== 'Running'}
		<Button
			onclick={() => handleStart()}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-green-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<span class="icon-[mdi--play] mr-1 h-4 w-4"></span>
			{'Start'}
		</Button>

		<Button
			onclick={() => {
				modalState.isDeleteOpen = true;
				modalState.title = `${vm.current.name} (${vm.current.rid})`;
			}}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! ml-2 h-6 text-black hover:bg-red-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>

			{'Delete'}
		</Button>
	{:else if (type === 'stop' || type === 'shutdown' || type === 'reboot') && domain.current.id !== -1 && domain.current.status === 'Running'}
		<Button
			onclick={() =>
				type === 'stop' ? handleStop() : type === 'shutdown' ? handleShutdown() : handleReboot()}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			{#if type === 'stop'}
				<div class="flex items-center">
					<span class="icon-[mdi--stop] mr-1 h-4 w-4"></span>
					<span>Stop</span>
				</div>
			{:else if type === 'shutdown'}
				<div class="flex items-center">
					<span class="icon-[mdi--power] mr-1 h-4 w-4"></span>
					<span>Shutdown</span>
				</div>
			{:else if type === 'reboot'}
				<div class="flex items-center">
					<span class="icon-[mdi--restart] mr-1 h-4 w-4"></span>
					<span>Reboot</span>
				</div>
			{/if}
		</Button>
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-1 border p-4">
		{@render button('start')}
		{@render button('reboot')}
		{@render button('shutdown')}
		{@render button('stop')}

		<!-- Towards the right we should have another button -->
		<div class="ml-auto flex h-full items-center">
			<SimpleSelect
				options={[
					{ label: 'Hourly', value: 'hourly' },
					{ label: 'Daily', value: 'daily' },
					{ label: 'Weekly', value: 'weekly' },
					{ label: 'Monthly', value: 'monthly' },
					{ label: 'Yearly', value: 'yearly' }
				]}
				bind:value={gfsStep}
				onChange={() => {
					stats.refetch();
				}}
				classes={{ trigger: 'h-6!' }}
				icon="icon-[mdi--calendar]"
			/>
		</div>
	</div>

	<div class="min-h-0 flex-1">
		<ScrollArea orientation="both" class="h-full">
			<div class="grid grid-cols-1 gap-4 p-4 lg:grid-cols-2">
				<Card.Root class="w-full gap-0 p-4">
					<Card.Header class="p-0">
						<Card.Description class="text-md  font-normal text-blue-600 dark:text-blue-500">
							<div class="flex items-center gap-1.5 whitespace-nowrap">
								{#if gaInfo.current && getVMIconByGaId(gaInfo.current?.osInfo.id || '')}
									<span class="icon {getVMIconByGaId(gaInfo.current?.osInfo.id || '')} h-6 w-6"
									></span>
								{/if}
								<span>{vm.current.name}</span>
								{#if udTime}
									<span>({udTime})</span>
								{/if}
							</div>
						</Card.Description>
					</Card.Header>
					<Card.Content class="mt-3 p-0">
						<div class="flex items-start">
							<div class="flex items-center">
								<span class="icon-[fluent--status-12-filled] mr-1 h-5 w-5"></span>
								{'Status'}
							</div>
							<div class="ml-auto">
								{domain.current.status}
							</div>
						</div>

						<div class="mt-2">
							<div class="flex w-full justify-between pb-1">
								<p class="inline-flex items-center">
									<span class="icon-[solar--cpu-bold] mr-1 h-5 w-5"></span>

									{'CPU Usage'}
								</p>
								<p class="ml-auto">
									{#if domain.current.status === 'Running'}
										{`${floatToNDecimals(recentStat.cpuUsage, 2)}% of ${vm.current.cpuCores * vm.current.cpuThreads * vm.current.cpuSockets} vCPU(s)`}
									{:else}
										{`0% of ${vm.current.cpuCores * vm.current.cpuThreads * vm.current.cpuSockets} vCPU(s)`}
									{/if}
								</p>
							</div>

							{#if domain.current.status === 'Running'}
								<Progress value={recentStat.cpuUsage || 0} max={100} class="ml-auto h-2" />
							{:else}
								<Progress value={0} max={100} class="ml-auto h-2" />
							{/if}
						</div>

						<div class="mt-2">
							<div class="flex w-full justify-between pb-1">
								<p class="inline-flex items-center">
									<span class="icon-[ph--memory] mr-1 h-5 w-5"></span>

									{'RAM Usage'}
								</p>
								<p class="ml-auto">
									{#if vm}
										{#if domain.current.status === 'Running'}
											{`${floatToNDecimals(recentStat.memoryUsage, 2)}% of ${humanFormat(vm.current.ram || 0)}`}
										{:else}
											{`0% of ${humanFormat(vm.current.ram || 0)}`}
										{/if}
									{/if}
								</p>
							</div>

							{#if domain.current.status === 'Running'}
								<Progress value={recentStat.memoryUsage || 0} max={100} class="ml-auto h-2" />
							{:else}
								<Progress value={0} max={100} class="ml-auto h-2" />
							{/if}
						</div>
					</Card.Content>
				</Card.Root>

				<Card.Root class="w-full gap-0 p-4">
					<Card.Header class="p-0">
						<Card.Description class="text-md font-normal text-blue-600 dark:text-blue-500">
							Description
						</Card.Description>
					</Card.Header>
					<Card.Content class="mt-3 p-0">
						<CustomValueInput
							label={''}
							placeholder="Notes about VM"
							bind:value={vmDescription}
							classes=""
							textAreaClasses="!h-32"
							type="textarea"
						/>
					</Card.Content>
				</Card.Root>
			</div>

			{#if gaInfo.current}
				<div class="space-y-4 px-4 pb-4">
					<Card.Root class="w-full gap-0 p-4">
						<Card.Header class="p-0">
							<Card.Description
								class="text-md flex items-center justify-between font-normal text-blue-600 dark:text-blue-500"
							>
								<div class="flex items-center gap-2">
									<span class="icon icon-[lucide--bot] h-6 w-6"></span>
									<span>Guest Information</span>
								</div>

								<div class="flex items-center gap-2">
									<SimpleSelect
										options={[
											{ label: 'OS Info', value: 'os' },
											{ label: 'Network', value: 'network' }
										]}
										bind:value={activeGaView}
										classes={{ trigger: 'h-6! text-white min-w-[100px]' }}
										onChange={(v: string) => {
											activeGaView = v;
										}}
									/>

									<Button
										variant="secondary"
										size="icon"
										class="h-5 w-5"
										onclick={() => gaInfo.refetch()}
										disabled={gaInfo.loading}
									>
										<span class="icon-[mdi--refresh] h-4 w-4 {gaInfo.loading ? 'animate-spin' : ''}"
										></span>
									</Button>
								</div>
							</Card.Description>
						</Card.Header>

						<Card.Content class="mt-3 p-0">
							{#if activeGaView === 'os'}
								<Table.Root class="w-full">
									<Table.Body>
										<Table.Row>
											<Table.Cell class="font-medium">OS Name</Table.Cell>
											<Table.Cell
												>{gaInfo.current.osInfo['pretty-name'] ||
													gaInfo.current.osInfo.name ||
													'Unknown'}</Table.Cell
											>
										</Table.Row>
										<Table.Row>
											<Table.Cell class="font-medium">Kernel</Table.Cell>
											<Table.Cell>{gaInfo.current.osInfo['kernel-release'] || '-'}</Table.Cell>
										</Table.Row>
										<Table.Row>
											<Table.Cell class="font-medium">Architecture</Table.Cell>
											<Table.Cell>{gaInfo.current.osInfo.machine || '-'}</Table.Cell>
										</Table.Row>
										<Table.Row>
											<Table.Cell class="font-medium">Version ID</Table.Cell>
											<Table.Cell>{gaInfo.current.osInfo['version-id'] || '-'}</Table.Cell>
										</Table.Row>
									</Table.Body>
								</Table.Root>
							{:else if activeGaView === 'network'}
								<div class="max-h-64 overflow-y-auto rounded-md border">
									<Table.Root class="w-full">
										<Table.Header class="sticky top-0 bg-muted/50 backdrop-blur-sm">
											<Table.Row>
												<Table.Head>Interface</Table.Head>
												<Table.Head>MAC Address</Table.Head>
												<Table.Head>IP Addresses</Table.Head>
												<Table.Head class="text-right">RX / TX</Table.Head>
											</Table.Row>
										</Table.Header>
										<Table.Body>
											{#if gaInfo.current.interfaces && gaInfo.current.interfaces.length > 0}
												{#each gaInfo.current.interfaces as iface}
													<Table.Row>
														<Table.Cell class="font-mono font-bold"
															>{iface.name || 'unknown'}</Table.Cell
														>
														<Table.Cell class="font-mono text-xs"
															>{iface['hardware-address'] || '-'}</Table.Cell
														>
														<Table.Cell>
															<div class="flex flex-col gap-1">
																{#if iface['ip-addresses'] && iface['ip-addresses'].length > 0}
																	{#each iface['ip-addresses'] as ip}
																		<span class="text-xs">
																			<span class="text-muted-foreground mr-1 uppercase"
																				>{ip['ip-address-type']}:</span
																			>
																			{ip['ip-address']}/{ip.prefix}
																		</span>
																	{/each}
																{:else}
																	<span class="text-muted-foreground text-xs">-</span>
																{/if}
															</div>
														</Table.Cell>
														<Table.Cell class="text-right text-xs">
															{#if iface.statistics}
																<div>↓ {humanFormat(iface.statistics['rx-bytes'] || 0)}B</div>
																<div>↑ {humanFormat(iface.statistics['tx-bytes'] || 0)}B</div>
															{:else}
																<span class="text-muted-foreground italic">N/A</span>
															{/if}
														</Table.Cell>
													</Table.Row>
												{/each}
											{:else}
												<Table.Row>
													<Table.Cell colspan={4} class="text-center text-muted-foreground py-4">
														No network interfaces reported by agent.
													</Table.Cell>
												</Table.Row>
											{/if}
										</Table.Body>
									</Table.Root>
								</div>
							{/if}
						</Card.Content>
					</Card.Root>
				</div>
			{/if}

			<div class="space-y-4 px-4 pb-4">
				<LineBrush
					title="CPU Usage"
					points={stats.current.map((data) => ({
						date: new Date(data.createdAt).getTime(),
						value: Number(data.cpuUsage)
					}))}
					percentage={true}
					color="one"
					containerContentHeight="h-64"
				/>

				<LineBrush
					title="Memory Usage"
					points={stats.current.map((data) => ({
						date: new Date(data.createdAt).getTime(),
						value: Number(data.memoryUsage)
					}))}
					percentage={true}
					color="two"
					containerContentHeight="h-64"
				/>
			</div>
		</ScrollArea>
	</div>
</div>

<AlertDialogRaw.Root bind:open={modalState.isDeleteOpen}>
	<AlertDialogRaw.Content onInteractOutside={(e) => e.preventDefault()} class="p-5">
		<AlertDialogRaw.Header>
			<AlertDialogRaw.Title>Are you sure?</AlertDialogRaw.Title>
			<AlertDialogRaw.Description>
				{`This will permanently delete VM`}
				<span class="font-semibold">{modalState?.title}.</span>

				<div class="flex flex-row space-x-4">
					<CustomCheckbox
						label="Delete MAC Object(s)"
						bind:checked={modalState.deleteMACs}
						classes="flex items-center gap-2 mt-4"
					></CustomCheckbox>

					<CustomCheckbox
						label="Delete RAW Disk(s)"
						bind:checked={modalState.deleteRAWDisks}
						classes="flex items-center gap-2 mt-4"
					></CustomCheckbox>

					<CustomCheckbox
						label="Delete Volume(s)"
						bind:checked={modalState.deleteVolumes}
						classes="flex items-center gap-2 mt-4"
					></CustomCheckbox>
				</div>
			</AlertDialogRaw.Description>
		</AlertDialogRaw.Header>
		<AlertDialogRaw.Footer>
			<AlertDialogRaw.Cancel
				onclick={() => {
					modalState.isDeleteOpen = false;
				}}>Cancel</AlertDialogRaw.Cancel
			>
			<AlertDialogRaw.Action onclick={handleDelete}>Continue</AlertDialogRaw.Action>
		</AlertDialogRaw.Footer>
	</AlertDialogRaw.Content>
</AlertDialogRaw.Root>

<LoadingDialog
	bind:open={modalState.loading.open}
	title={modalState.loading.title}
	description={modalState.loading.description}
	iconColor={modalState.loading.iconColor}
/>
