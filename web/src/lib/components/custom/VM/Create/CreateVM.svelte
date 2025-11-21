<script lang="ts">
	import { getSwitches } from '$lib/api/network/switch';
	import { getPCIDevices, getPPTDevices } from '$lib/api/system/pci';
	import { getDownloadsByUType } from '$lib/api/utilities/downloader';
	import { getVMs, newVM } from '$lib/api/vm/vm';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import type { SwitchList } from '$lib/types/network/switch';
	import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
	import type { UTypeGroupedDownload } from '$lib/types/utilities/downloader';
	import { generatePassword } from '$lib/utils/string';
	import { getNextId, isValidCreateData } from '$lib/utils/vm/vm';
	import Advanced from './Advanced.svelte';
	import Basic from './Basic.svelte';
	import Hardware from './Hardware.svelte';
	import Network from './Network.svelte';
	import Storage from './Storage.svelte';
	import { getNodes } from '$lib/api/cluster/cluster';
	import { getJails } from '$lib/api/jail/jail';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { reload as reloadStore } from '$lib/stores/api.svelte';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import type { Jail } from '$lib/types/jail/jail';
	import type { NetworkObject } from '$lib/types/network/object';
	import { type CPUPin, type CreateData, type VM } from '$lib/types/vm/vm';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { getBasicSettings } from '$lib/api/basic';
	import type { BasicSettings } from '$lib/types/basic';
	import { useQueries } from '$lib/runes/useQuery.svelte';

	interface Props {
		open: boolean;
	}

	let { open = $bindable() }: Props = $props();

	const {
		networkObjects: networkObjectsQuery,
		networkSwitches: networkSwitchesQuery,
		pciDevices: pciDevicesQuery,
		pptDevices: pptDevicesQuery,
		downloadsByUType: downloadsByUTypeQuery,
		vms: vmsQuery,
		jails: jailsQuery,
		clusterNodes: clusterNodesQuery,
		basicSettings: basicSettingsQuery,
		refetchAll
	} = useQueries(() => ({
		networkObjects: () => ({
			key: 'network-objects',
			queryFn: async () => await getNetworkObjects(),
			initialData: [] as NetworkObject[],
			onSuccess: (networkObjects) => updateCache('network-objects', networkObjects)
		}),
		networkSwitches: () => ({
			key: 'network-switches',
			queryFn: async () => await getSwitches(),
			initialData: {} as SwitchList,
			onSuccess: (networkSwitches) => updateCache('network-switches', networkSwitches)
		}),
		pciDevices: () => ({
			key: 'pci-devices',
			queryFn: async () => (await getPCIDevices()) as PCIDevice[],
			initialData: [] as PCIDevice[],
			onSuccess: (pciDevices) => updateCache('pci-devices', pciDevices)
		}),
		pptDevices: () => ({
			key: 'ppt-devices',
			queryFn: async () => (await getPPTDevices()) as PPTDevice[],
			initialData: [] as PPTDevice[],
			onSuccess: (pptDevices) => updateCache('ppt-devices', pptDevices)
		}),
		downloadsByUType: () => ({
			key: 'downloads-by-utype',
			queryFn: async () => await getDownloadsByUType(),
			initialData: [] as UTypeGroupedDownload[],
			onSuccess: (downloads) => updateCache('downloads-by-utype', downloads)
		}),
		vms: () => ({
			key: 'vm-list',
			queryFn: async () => await getVMs(),
			initialData: [] as VM[],
			onSuccess: (vms) => updateCache('vm-list', vms)
		}),
		jails: () => ({
			key: 'jail-list',
			queryFn: async () => await getJails(),
			initialData: [] as Jail[],
			onSuccess: (jails) => updateCache('jail-list', jails)
		}),
		clusterNodes: () => ({
			key: 'cluster-nodes',
			queryFn: async () => await getNodes(),
			initialData: [] as ClusterNode[],
			onSuccess: (nodes) => updateCache('cluster-nodes', nodes)
		}),
		basicSettings: () => ({
			key: 'basic-settings',
			queryFn: async () => await getBasicSettings(),
			initialData: {
				pools: [],
				services: [],
				initialized: false
			} as BasicSettings,
			onSuccess: (settings) => updateCache('basic-settings', settings)
		})
	}));

	let reload = $state(false);

	$effect(() => {
		if (reload) {
			refetchAll();
			reload = false;
		}
	});

	let networkObjects = $derived(networkObjectsQuery.data as NetworkObject[]);
	let networkSwitches: SwitchList = $derived(networkSwitchesQuery.data as SwitchList);
	let pciDevices = $derived(pciDevicesQuery.data as PCIDevice[]);
	let pptDevices = $derived(pptDevicesQuery.data as PPTDevice[]);
	let downloads = $derived(downloadsByUTypeQuery.data as UTypeGroupedDownload[]);
	let vms = $derived(vmsQuery.data as VM[]);
	let jails = $derived(jailsQuery.data as Jail[]);
	let nodes = $derived(clusterNodesQuery.data as ClusterNode[]);
	let basicSettings = $derived(basicSettingsQuery.data as BasicSettings);
	let passablePci: PCIDevice[] = $derived.by(() => {
		return pciDevices.filter((device) => device.name.startsWith('ppt'));
	});

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
			pool: '',
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
			pinnedCPUs: [] as CPUPin[],
			isPinningOpen: false
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
			timeOffset: 'utc' as 'utc' | 'localtime',
			cloudInit: {
				enabled: false,
				data: '',
				metadata: ''
			},
			ignoreUmsrs: false
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
		if (isValidCreateData(data, downloads)) {
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

			reloadStore.leftPanel = true;
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
					<span class="icon-[material-symbols--monitor-outline-rounded] h-5 w-5"></span>
					Create Virtual Machine
				</div>
				<div class="flex items-center gap-0.5">
					<Button size="sm" variant="link" class="h-4" onclick={() => resetModal()} title={'Reset'}>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>

						<span class="sr-only">{'Reset'}</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						onclick={() => (open = false)}
						title={'Close'}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
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
									bind:refetch={reload}
								/>
							{:else if value === 'storage'}
								<Storage
									{downloads}
									pools={basicSettings.pools}
									bind:type={modal.storage.type}
									bind:pool={modal.storage.pool}
									bind:size={modal.storage.size}
									bind:emulation={modal.storage.emulation}
									bind:iso={modal.storage.iso}
									cloudInit={modal.advanced.cloudInit}
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
									bind:isPinningOpen={modal.hardware.isPinningOpen}
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
									bind:cloudInit={modal.advanced.cloudInit}
									bind:ignoreUmsrs={modal.advanced.ignoreUmsrs}
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
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					{:else}
						Create Virtual Machine
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
