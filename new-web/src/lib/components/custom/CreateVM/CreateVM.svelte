<script lang="ts">
	import { getInterfaces } from '$lib/api/network/iface';
	import { getSwitches } from '$lib/api/network/switch';
	import { getPCIDevices, getPPTDevices } from '$lib/api/system/pci';
	import { getDownloads } from '$lib/api/utilities/downloader';
	import { newVM } from '$lib/api/vm/vm';
	import { getDatasets } from '$lib/api/zfs/datasets';
	import { getPools } from '$lib/api/zfs/pool';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import type { SwitchList } from '$lib/types/network/switch';
	import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { getTranslation } from '$lib/utils/i18n';
	import { capitalizeFirstLetter, generatePassword } from '$lib/utils/string';
	import { isValidCreateData } from '$lib/utils/vm/vm';
	import Icon from '@iconify/svelte';
	import { useQueries } from '@sveltestack/svelte-query';
	import Advanced from './Advanced.svelte';
	import Basic from './Basic.svelte';
	import Hardware from './Hardware.svelte';
	import Network from './Network.svelte';
	import Storage from './Storage.svelte';

	import { type CreateData } from '$lib/types/vm/vm';
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
	}

	let { open = $bindable() }: Props = $props();

	const results = useQueries([
		{
			queryKey: ['poolList-svm'],
			queryFn: async () => {
				return await getPools();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: [],
			refetchOnMount: 'always'
		},
		{
			queryKey: ['datasetList-svm'],
			queryFn: async () => {
				return await getDatasets();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: [],
			refetchOnMount: 'always'
		},
		{
			queryKey: ['networkInterfaces-svm'],
			queryFn: async () => {
				return await getInterfaces();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: [],
			refetchOnMount: 'always'
		},
		{
			queryKey: ['networkSwitches-svm'],

			queryFn: async () => {
				return await getSwitches();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: {} as SwitchList,
			refetchOnMount: 'always'
		},
		{
			queryKey: ['pciDevices-svm'],
			queryFn: async () => {
				return (await getPCIDevices()) as PCIDevice[];
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: [] as PCIDevice[],
			refetchOnMount: 'always'
		},
		{
			queryKey: ['pptDevices-svm'],
			queryFn: async () => {
				return (await getPPTDevices()) as PPTDevice[];
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: [] as PPTDevice[],
			refetchOnMount: 'always'
		},
		{
			queryKey: ['downloads-svm'],
			queryFn: async () => {
				return await getDownloads();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: [],
			refetchOnMount: 'always'
		}
	]);

	let datasets: Dataset[] = $derived($results[1].data as Dataset[]);
	let volumes: Dataset[] = $derived(datasets.filter((dataset) => dataset.type === 'volume'));
	let filesystems: Dataset[] = $derived(
		datasets.filter((dataset) => dataset.type === 'filesystem')
	);

	let networkSwitches: SwitchList = $derived($results[3].data as SwitchList);
	let pciDevices: PCIDevice[] = $derived($results[4].data as PCIDevice[]);
	let pptDevices: PPTDevice[] = $derived($results[5].data as PPTDevice[]);
	let passablePci: PCIDevice[] = $derived(
		pciDevices.filter((device) => device.name.startsWith('ppt'))
	);

	let downloads = $derived($results[6].data as Download[]);

	const tabs = [
		{ value: 'basic', label: 'Basic' },
		{ value: 'storage', label: 'Storage' },
		{ value: 'network', label: 'Network' },
		{ value: 'hardware', label: 'Hardware' },
		{ value: 'advanced', label: 'Advanced' }
	];

	let modal: CreateData = $state({
		name: '',
		id: 0,
		description: '',
		storage: {
			type: 'zvol',
			guid: '',
			size: 0,
			emulation: 'ahci-hd',
			iso: ''
		},
		network: {
			switch: 0,
			mac: '',
			emulation: 'e1000'
		},
		hardware: {
			sockets: 1,
			cores: 1,
			threads: 1,
			memory: 0,
			passthroughIds: [] as number[]
		},
		advanced: {
			vncPort: 0,
			vncPassword: generatePassword(),
			vncWait: false,
			vncResolution: '1024x768',
			startAtBoot: false,
			bootOrder: 0
		}
	});

	async function create() {
		const data = $state.snapshot(modal);
		if (isValidCreateData(data)) {
			const response = await newVM(data);
			if (response.status === 'success') {
				toast.success(`Created VM ${modal.name}`, {
					duration: 3000,
					position: 'bottom-center'
				});
				open = false;
			} else {
				toast.error('Failed to create VM', {
					duration: 3000,
					position: 'bottom-center'
				});
			}
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed left-1/2 top-1/2 max-h-[90vh] w-full max-w-[90%] -translate-x-1/2 -translate-y-1/2 transform overflow-visible overflow-y-auto p-5 transition-all duration-300 ease-in-out lg:max-w-xl"
	>
		<div class="flex items-center justify-between px-2 py-3">
			<Dialog.Header class="p-0">
				<Dialog.Title class="flex flex-col gap-1 text-left">
					<div class="flex items-center gap-2">
						<Icon icon="material-symbols:monitor-outline-rounded" class="h-5 w-5 " />
						Create Virtual Machine
					</div>
					<p class="text-muted-foreground text-sm">
						Configure your virtual machine with custom hardware and network settings
					</p>
				</Dialog.Title>
			</Dialog.Header>

			<div class="flex items-center gap-0.5">
				<Button
					size="sm"
					variant="ghost"
					class="h-8"
					title={capitalizeFirstLetter(getTranslation('common.reset', 'Reset'))}
				>
					<Icon icon="radix-icons:reset" class="pointer-events-none h-4 w-4" />
					<span class="sr-only"
						>{capitalizeFirstLetter(getTranslation('common.reset', 'Reset'))}</span
					>
				</Button>
				<Button
					size="sm"
					variant="ghost"
					class="h-8"
					onclick={() => (open = false)}
					title={capitalizeFirstLetter(getTranslation('common.close', 'Close'))}
				>
					<Icon icon="material-symbols:close-rounded" class="pointer-events-none h-4 w-4" />
					<span class="sr-only"
						>{capitalizeFirstLetter(getTranslation('common.close', 'Close'))}</span
					>
				</Button>
			</div>
		</div>

		<Tabs.Root value="basic" class="w-full overflow-hidden">
			<Tabs.List class="grid w-full grid-cols-5 p-0 px-2">
				{#each tabs as { value, label }}
					<Tabs.Trigger class="border-b" {value}>{label}</Tabs.Trigger>
				{/each}
			</Tabs.List>

			{#each tabs as { value, label }}
				<Tabs.Content {value} class="">
					<div class="">
						{#if value === 'basic'}
							<Basic
								bind:name={modal.name}
								bind:id={modal.id}
								bind:description={modal.description}
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
								bind:switch={modal.network.switch}
								bind:mac={modal.network.mac}
								bind:emulation={modal.network.emulation}
							/>
						{:else if value === 'hardware'}
							<Hardware
								devices={passablePci}
								{pptDevices}
								bind:sockets={modal.hardware.sockets}
								bind:cores={modal.hardware.cores}
								bind:threads={modal.hardware.threads}
								bind:memory={modal.hardware.memory}
								bind:passthroughIds={modal.hardware.passthroughIds}
							/>
						{:else if value === 'advanced'}
							<Advanced
								bind:vncPort={modal.advanced.vncPort}
								bind:vncPassword={modal.advanced.vncPassword}
								bind:vncWait={modal.advanced.vncWait}
								bind:startAtBoot={modal.advanced.startAtBoot}
								bind:bootOrder={modal.advanced.bootOrder}
								bind:vncResolution={modal.advanced.vncResolution}
							/>
						{/if}
					</div>
				</Tabs.Content>
			{/each}
		</Tabs.Root>

		<Dialog.Footer>
			<div class="flex w-full justify-end px-1 py-3 md:flex-row">
				<Button size="sm" type="button" class="h-8" onclick={() => create()}
					>Create Virtual Machine</Button
				>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
