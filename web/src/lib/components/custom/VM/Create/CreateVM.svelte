<script lang="ts">
	import { getInterfaces } from '$lib/api/network/iface';
	import { getSwitches } from '$lib/api/network/switch';
	import { getPCIDevices, getPPTDevices } from '$lib/api/system/pci';
	import { getDownloads } from '$lib/api/utilities/downloader';
	import { getVMs, newVM } from '$lib/api/vm/vm';
	import { getDatasets } from '$lib/api/zfs/datasets';
	import { getPools } from '$lib/api/zfs/pool';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import type { SwitchList } from '$lib/types/network/switch';
	import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { generatePassword } from '$lib/utils/string';
	import { getNextId, isValidCreateData } from '$lib/utils/vm/vm';
	import Icon from '@iconify/svelte';
	import { useQueries, useQueryClient } from '@sveltestack/svelte-query';
	import Advanced from './Advanced.svelte';
	import Basic from './Basic.svelte';
	import Hardware from './Hardware.svelte';
	import Network from './Network.svelte';
	import Storage from './Storage.svelte';

	import { getNodes } from '$lib/api/cluster/cluster';
	import { getJails } from '$lib/api/jail/jail';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { reload } from '$lib/stores/api.svelte';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import type { Jail } from '$lib/types/jail/jail';
	import type { NetworkObject } from '$lib/types/network/object';
	import { type CreateData, type VM } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
	}

	let { open = $bindable() }: Props = $props();

	const queryClient = useQueryClient();
	const results = useQueries([
		{
			queryKey: 'pool-list',
			queryFn: async () => {
				return await getPools();
			},
			keepPreviousData: true,
			initialData: [],
			refetchOnMount: 'always'
		},
		{
			queryKey: 'zfs-filesystems',
			queryFn: async () => {
				return await getDatasets('filesystem');
			},
			keepPreviousData: true,
			initialData: [],
			refetchOnMount: 'always'
		},
		{
			queryKey: 'zfs-volumes',
			queryFn: async () => {
				return await getDatasets('volume');
			},
			keepPreviousData: true,
			initialData: [],
			refetchOnMount: 'always'
		},
		{
			queryKey: 'network-interfaces',
			queryFn: async () => {
				return await getInterfaces();
			},
			keepPreviousData: true,
			initialData: [],
			refetchOnMount: 'always'
		},
		{
			queryKey: 'network-switches',
			queryFn: async () => {
				return await getSwitches();
			},
			keepPreviousData: true,
			initialData: {} as SwitchList,
			refetchOnMount: 'always'
		},
		{
			queryKey: 'pci-devices',
			queryFn: async () => {
				return (await getPCIDevices()) as PCIDevice[];
			},
			keepPreviousData: true,
			initialData: [] as PCIDevice[],
			refetchOnMount: 'always'
		},
		{
			queryKey: 'ppt-devices',
			queryFn: async () => {
				return (await getPPTDevices()) as PPTDevice[];
			},
			keepPreviousData: true,
			initialData: [] as PPTDevice[],
			refetchOnMount: 'always'
		},
		{
			queryKey: 'downloads',
			queryFn: async () => {
				return await getDownloads();
			},
			keepPreviousData: true,
			initialData: [],
			refetchOnMount: 'always'
		},
		{
			queryKey: 'vm-list',
			queryFn: async () => {
				return await getVMs();
			},
			keepPreviousData: true,
			initialData: [],
			refetchOnMount: 'always'
		},
		{
			queryKey: 'network-objects',
			queryFn: async () => {
				return await getNetworkObjects();
			},
			keepPreviousData: true,
			initialData: [],
			refetchOnMount: 'always'
		},
		{
			queryKey: 'jail-list',
			queryFn: async () => {
				return await getJails();
			},
			keepPreviousData: true,
			initialData: [],
			refetchOnMount: 'always'
		},
		{
			queryKey: 'cluster-nodes',
			queryFn: async () => {
				return await getNodes();
			},
			keepPreviousData: true,
			initialData: [],
			refetchOnMount: 'always'
		}
	]);

	let refetch = $state(false);

	$effect(() => {
		if (refetch) {
			queryClient.refetchQueries('pool-list');
			queryClient.refetchQueries('zfs-filesystems');
			queryClient.refetchQueries('zfs-volumes');
			queryClient.refetchQueries('network-interfaces');
			queryClient.refetchQueries('network-switches');
			queryClient.refetchQueries('pci-devices');
			queryClient.refetchQueries('ppt-devices');
			queryClient.refetchQueries('downloads');
			queryClient.refetchQueries('vm-list');
			queryClient.refetchQueries('network-objects');
			queryClient.refetchQueries('jail-list');
			queryClient.refetchQueries('cluster-nodes');

			refetch = false;
		}
	});

	let vms: VM[] = $derived($results[8].data as VM[]);
	let jails: Jail[] = $derived($results[10].data as Jail[]);
	let filesystems: Dataset[] = $derived($results[1].data as Dataset[]);
	let volumes: Dataset[] = $derived($results[2].data as Dataset[]);

	let networkSwitches: SwitchList = $derived($results[4].data as SwitchList);
	let pciDevices: PCIDevice[] = $derived($results[5].data as PCIDevice[]);
	let pptDevices: PPTDevice[] = $derived($results[6].data as PPTDevice[]);
	let networkObjects = $derived($results[9].data as NetworkObject[]);
	let passablePci: PCIDevice[] = $derived.by(() => {
		return pciDevices.filter((device) => device.name.startsWith('ppt'));
	});

	let downloads = $derived($results[7].data as Download[]);
	let nodes = $derived($results[11].data as ClusterNode[]);

	const tabs = [
		{ value: 'basic', label: 'Basic' },
		{ value: 'storage', label: 'Storage' },
		{ value: 'network', label: 'Network' },
		{ value: 'hardware', label: 'Hardware' },
		{ value: 'advanced', label: 'Advanced' }
	];

	let options = {
		name: '',
		id: 0,
		description: '',
		node: '',
		storage: {
			type: 'zvol',
			guid: '',
			size: 0,
			emulation: 'ahci-hd',
			iso: ''
		},
		network: {
			switch: 'None',
			mac: '',
			emulation: 'e1000'
		},
		hardware: {
			sockets: 1,
			cores: 1,
			threads: 1,
			memory: 0,
			passthroughIds: [] as number[],
			pinnedCPUs: [] as number[]
		},
		advanced: {
			serial: false,
			vncPort: 0,
			vncPassword: generatePassword(),
			vncWait: false,
			vncResolution: '1024x768',
			startAtBoot: false,
			bootOrder: 0,
			tpmEmulation: false,
			timeOffset: 'utc' as 'utc' | 'localtime'
		}
	};

	let nextId = $derived(getNextId(vms, jails));
	let modal: CreateData = $state(options);
	let loading = $state(false);

	$effect(() => {
		modal.id = nextId;
	});

	async function create() {
		const data = $state.snapshot(modal);
		if (isValidCreateData(data)) {
			loading = true;
			const response = await newVM(data);
			loading = false;
			if (response.status === 'success') {
				toast.success(`Created VM ${modal.name}`, {
					duration: 3000,
					position: 'bottom-center'
				});
				open = false;
			} else {
				handleAPIError(response);
				toast.error('Failed to create VM', {
					duration: 3000,
					position: 'bottom-center'
				});
			}

			reload.leftPanel = true;
		}
	}

	function resetModal() {
		modal = options;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed left-1/2 top-1/2 flex h-[85vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform flex-col gap-0  overflow-auto p-5 transition-all duration-300 ease-in-out lg:h-[72vh] lg:max-w-2xl"
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex  justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<Icon icon="material-symbols:monitor-outline-rounded" class="h-5 w-5 " />
					Create Virtual Machine
				</div>
				<div class="flex items-center gap-0.5">
					<Button size="sm" variant="link" class="h-4" onclick={() => resetModal()} title={'Reset'}>
						<Icon icon="radix-icons:reset" class="pointer-events-none h-4 w-4" />
						<span class="sr-only">{'Reset'}</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						onclick={() => (open = false)}
						title={'Close'}
					>
						<Icon icon="material-symbols:close-rounded" class="pointer-events-none h-4 w-4" />
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-6 flex-1 overflow-y-auto">
			<Tabs.Root value="basic" class="w-full overflow-hidden">
				<Tabs.List class="grid w-full grid-cols-5 p-0 ">
					{#each tabs as { value, label }}
						<Tabs.Trigger class="border-b" {value}>{label}</Tabs.Trigger>
					{/each}
				</Tabs.List>

				{#each tabs as { value, label }}
					<Tabs.Content {value}>
						<div>
							{#if value === 'basic'}
								<Basic
									bind:name={modal.name}
									bind:node={modal.node}
									bind:id={modal.id}
									bind:description={modal.description}
									{nodes}
									bind:refetch
								/>
							{:else if value === 'storage'}
								<Storage
									{volumes}
									{filesystems}
									{downloads}
									bind:type={modal.storage.type}
									bind:guid={modal.storage.guid}
									bind:size={modal.storage.size}
									bind:emulation={modal.storage.emulation}
									bind:iso={modal.storage.iso}
								/>
							{:else if value === 'network'}
								<Network
									switches={networkSwitches}
									{vms}
									{networkObjects}
									bind:switch={modal.network.switch}
									bind:mac={modal.network.mac}
									bind:emulation={modal.network.emulation}
								/>
							{:else if value === 'hardware'}
								<Hardware
									devices={passablePci}
									{vms}
									{pptDevices}
									bind:sockets={modal.hardware.sockets}
									bind:cores={modal.hardware.cores}
									bind:threads={modal.hardware.threads}
									bind:memory={modal.hardware.memory}
									bind:passthroughIds={modal.hardware.passthroughIds}
									bind:pinnedCPUs={modal.hardware.pinnedCPUs}
								/>
							{:else if value === 'advanced'}
								<Advanced
									bind:serial={modal.advanced.serial}
									bind:vncPort={modal.advanced.vncPort}
									bind:vncPassword={modal.advanced.vncPassword}
									bind:vncWait={modal.advanced.vncWait}
									bind:startAtBoot={modal.advanced.startAtBoot}
									bind:bootOrder={modal.advanced.bootOrder}
									bind:vncResolution={modal.advanced.vncResolution}
									bind:tpmEmulation={modal.advanced.tpmEmulation}
									bind:timeOffset={modal.advanced.timeOffset}
								/>
							{/if}
						</div>
					</Tabs.Content>
				{/each}
			</Tabs.Root>
		</div>

		<Dialog.Footer>
			<div class="flex w-full justify-end md:flex-row">
				<Button size="sm" type="button" class="h-8" onclick={() => create()} disabled={loading}>
					{#if loading}
						<Icon icon="mdi:loading" class="h-4 w-4 animate-spin" />
					{:else}
						Create Virtual Machine
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
