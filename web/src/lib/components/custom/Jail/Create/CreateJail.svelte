<script lang="ts">
	import { getNodes } from '$lib/api/cluster/cluster';
	import { getSimpleJails, newJail } from '$lib/api/jail/jail';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { getSwitches } from '$lib/api/network/switch';
	import { getDownloads } from '$lib/api/utilities/downloader';
	import { getSimpleVMs } from '$lib/api/vm/vm';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import type { CreateData } from '$lib/types/jail/jail';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { isValidCreateData } from '$lib/utils/jail/jail';
	import { getNextId } from '$lib/utils/vm/vm';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import Basic from './Basic.svelte';
	import Hardware from './Hardware.svelte';
	import Advanced from './Advanced.svelte';
	import Network from './Network.svelte';
	import Storage from './Storage.svelte';
	import { getPools } from '$lib/api/zfs/pool';

	interface Props {
		open: boolean;
		minimize: boolean;
	}

	let { open = $bindable(), minimize = $bindable() }: Props = $props();
	const tabs = [
		{ value: 'basic', label: 'Basic' },
		{ value: 'storage', label: 'Storage' },
		{ value: 'network', label: 'Network' },
		{ value: 'hardware', label: 'Hardware' },
		{ value: 'advanced', label: 'Advanced' }
	];

	let downloads = resource(
		() => 'downloads',
		async (key, prevKey, { signal }) => {
			const downloads = await getDownloads();
			updateCache(key, downloads);
			return downloads;
		}
	);

	let networkSwitches = resource(
		() => 'network-switches',
		async (key, prevKey, { signal }) => {
			const switches = await getSwitches();
			updateCache(key, switches);
			return switches;
		}
	);

	let networkObjects = resource(
		() => 'network-objects',
		async (key, prevKey, { signal }) => {
			const objects = await getNetworkObjects();
			updateCache(key, objects);
			return objects;
		}
	);

	let vms = resource(
		() => 'simple-vm-list',
		async (key, prevKey, { signal }) => {
			const vms = await getSimpleVMs();
			updateCache(key, vms);
			return vms;
		}
	);

	let jails = resource(
		() => 'simple-jail-list',
		async (key, prevKey, { signal }) => {
			const jails = await getSimpleJails();
			updateCache(key, jails);
			return jails;
		}
	);

	let nodes = resource(
		() => 'cluster-nodes',
		async (key, prevKey, { signal }) => {
			const nodes = await getNodes();
			updateCache(key, nodes);
			return nodes;
		}
	);

	let pools = resource(
		() => 'pool-list',
		async (key, prevKey, { signal }) => {
			const pools = await getPools();
			updateCache(key, pools);
			return pools;
		}
	);

	let refetch = $state(false);

	watch(
		() => refetch,
		(value) => {
			if (value) {
				downloads.refetch();
				networkSwitches.refetch();
				networkObjects.refetch();
				vms.refetch();
				jails.refetch();
				nodes.refetch();
				pools.refetch();

				refetch = false;
			}
		}
	);

	let creating: boolean = $state(false);

	let options = {
		name: '',
		hostname: '',
		id: 0,
		node: '',
		description: '',
		storage: {
			pool: '',
			base: '',
			fstab: ''
		},
		network: {
			switch: 'None',
			mac: 0,
			inheritIPv4: true,
			inheritIPv6: true,
			ipv4: 0,
			ipv4Gateway: 0,
			ipv6: 0,
			ipv6Gateway: 0,
			dhcp: false,
			slaac: false
		},
		hardware: {
			cpuCores: 1,
			ram: 0,
			startAtBoot: false,
			resourceLimits: true,
			bootOrder: 0,
			devfsRuleset: ''
		},
		advanced: {
			jailType: 'freebsd' as 'linux' | 'freebsd',
			additionalOptions: '',
			cleanEnvironment: true,
			execScripts: {
				prestart: { enabled: false, script: '' },
				start: { enabled: false, script: '' },
				poststart: { enabled: false, script: '' },
				prestop: { enabled: false, script: '' },
				stop: { enabled: false, script: '' },
				poststop: { enabled: false, script: '' }
			},
			allowedOptions: [] as string[],
			metadata: {
				env: '',
				meta: ''
			}
		}
	};

	let nextId = $derived.by(() => {
		if (vms.current && jails.current) {
			return getNextId(vms.current, jails.current);
		}

		return 137;
	});

	let modal: CreateData = $state(options);

	watch(
		() => nextId,
		(id) => {
			modal.id = id;
		}
	);

	function resetModal() {
		modal = options;
	}

	async function create() {
		const data = $state.snapshot(modal);

		if (data.hardware.resourceLimits === false) {
			data.hardware.cpuCores = 0;
			data.hardware.ram = 0;
		}

		if (!(await isValidCreateData(data))) {
			return;
		} else {
			creating = true;
			const response = await newJail(data);
			creating = false;

			if (response.error) {
				handleAPIError(response);
				let error = 'Failed to create jail';

				switch (response.error) {
					case 'failed_to_create: invalid_ipv4_gateway_or_address':
						error = 'Invalid IPv4 gateway or address';
						break;
					case 'failed_to_create: invalid_ipv6_gateway_or_address':
						error = 'Invalid IPv6 gateway or address';
						break;
					default:
						error = 'Failed to create jail';
				}

				reload.leftPanel = true;
				toast.error(error, {
					position: 'bottom-center'
				});
				return;
			}

			open = false;
			reload.leftPanel = true;

			toast.success(`Jail ${data.name} created`, {
				position: 'bottom-center'
			});
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed left-1/2 top-1/2 flex h-[85vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform flex-col gap-0  overflow-auto p-5 transition-all duration-300 ease-in-out lg:h-[72vh] lg:max-w-2xl"
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex  justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[hugeicons--prison] h-5 w-5"></span>
					<span>Create Jail</span>
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
						onclick={() => {
							minimize = true;
							open = false;
						}}
						title={'Minimize'}
					>
						<span class="icon-[mdi--window-minimize] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Minimize'}</span>
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
				<Tabs.List class="grid w-full grid-cols-5 p-0">
					{#each tabs as { value, label }}
						<Tabs.Trigger class="border-b" {value}>{label}</Tabs.Trigger>
					{/each}
				</Tabs.List>

				{#each tabs as { value, label }}
					<Tabs.Content {value}>
						<div>
							{#if value === 'basic' && nodes.current}
								<Basic
									bind:name={modal.name}
									bind:id={modal.id}
									bind:hostname={modal.hostname}
									bind:description={modal.description}
									bind:refetch
									bind:node={modal.node}
									nodes={nodes.current}
								/>
							{:else if value === 'storage' && pools.current && downloads.current && jails.current}
								<Storage
									downloads={downloads.current}
									pools={pools.current}
									ctId={modal.id}
									bind:pool={modal.storage.pool}
									bind:base={modal.storage.base}
									bind:fstab={modal.storage.fstab}
								/>
							{:else if value === 'network' && networkSwitches.current && networkObjects.current}
								<Network
									bind:switch={modal.network.switch}
									bind:mac={modal.network.mac}
									bind:inheritIPv4={modal.network.inheritIPv4}
									bind:inheritIPv6={modal.network.inheritIPv6}
									bind:ipv4={modal.network.ipv4}
									bind:ipv4Gateway={modal.network.ipv4Gateway}
									bind:ipv6={modal.network.ipv6}
									bind:ipv6Gateway={modal.network.ipv6Gateway}
									bind:dhcp={modal.network.dhcp}
									bind:slaac={modal.network.slaac}
									switches={networkSwitches.current}
									networkObjects={networkObjects.current}
								/>
							{:else if value === 'hardware'}
								<Hardware
									bind:cpuCores={modal.hardware.cpuCores}
									bind:ram={modal.hardware.ram}
									bind:startAtBoot={modal.hardware.startAtBoot}
									bind:bootOrder={modal.hardware.bootOrder}
									bind:resourceLimits={modal.hardware.resourceLimits}
									bind:devfsRuleset={modal.hardware.devfsRuleset}
								/>
							{:else if value === 'advanced'}
								<Advanced
									bind:jailType={modal.advanced.jailType}
									bind:additionalOptions={modal.advanced.additionalOptions}
									bind:cleanEnvironment={modal.advanced.cleanEnvironment}
									bind:execScripts={modal.advanced.execScripts}
									bind:allowedOptions={modal.advanced.allowedOptions}
									bind:metadata={modal.advanced.metadata}
								/>
							{/if}
						</div>
					</Tabs.Content>
				{/each}
			</Tabs.Root>
		</div>

		<Dialog.Footer>
			<div class="flex w-full justify-end md:flex-row">
				<Button size="sm" type="button" class="h-8" onclick={() => create()} disabled={creating}>
					<!-- Create Jail -->
					{#if creating}
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					{:else}
						Create Jail
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
