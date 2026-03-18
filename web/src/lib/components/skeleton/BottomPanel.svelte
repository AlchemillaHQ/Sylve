<script lang="ts">
	import { storage } from '$lib';
	import { getNodes } from '$lib/api/cluster/cluster';
	import { getAuditRecords } from '$lib/api/info/audit';
	import { getActiveLifecycleTasks } from '$lib/api/task/lifecycle';
	import { getSimpleVMs } from '$lib/api/vm/vm';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import * as Table from '$lib/components/ui/table/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import type { LifecycleTask } from '$lib/types/task/lifecycle';
	import { updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { resource, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import { fade } from 'svelte/transition';

	interface Props {
		clustered?: boolean;
		onLifecycleActiveChange?: (active: boolean) => void;
	}

	let { clustered = false, onLifecycleActiveChange }: Props = $props();

	let selectedHostname = $state(storage.hostname || '');
	const effectiveHostname = $derived(selectedHostname || storage.hostname || '');

	const clusterNodes = resource(
		() => `cluster-nodes-for-audit-${clustered ? 'clustered' : 'single'}`,
		async () => {
			if (!clustered) {
				return [];
			}

			return await getNodes();
		},
		{
			initialValue: [] as ClusterNode[]
		}
	);

	const hostnameOptions = $derived.by(() => {
		const names = new Set<string>();

		if (storage.hostname) {
			names.add(storage.hostname);
		}

		for (const node of clusterNodes.current) {
			if (node.hostname) {
				names.add(node.hostname);
			}
		}

		return Array.from(names)
			.sort((a, b) => a.localeCompare(b))
			.map((hostname) => ({
				value: hostname,
				label: hostname
			}));
	});

	const auditRecords = resource(
		() => `audit-record-${effectiveHostname || 'default'}`,
		async (key, prevKey, { signal }) => {
			const results = await getAuditRecords(effectiveHostname || undefined);
			updateCache(key, results);
			return results;
		}
	);

	const simpleVmList = resource(
		() => 'simple-vm-list',
		async (key, prevKey, { signal }) => {
			const results = await getSimpleVMs();
			updateCache(key, results);
			return results;
		}
	);

	const activeLifecycleTasks = resource(
		() => `active-lifecycle-tasks-${effectiveHostname || 'default'}`,
		async () => {
			return await getActiveLifecycleTasks(undefined, undefined, effectiveHostname || undefined);
		},
		{
			initialValue: [] as LifecycleTask[]
		}
	);

	useInterval(() => 2000, {
		callback: () => {
			activeLifecycleTasks.refetch();
		}
	});

	watch(
		() => reload.auditLog,
		(value) => {
			if (value) {
				auditRecords.refetch().then(() => {
					reload.auditLog = false;
				});
			}
		}
	);

	watch(
		() => storage.hostname,
		(hostname) => {
			if (!selectedHostname && hostname) {
				selectedHostname = hostname;
			}
		}
	);

	const pathToActionMap: Record<string, string> = $derived({
		'/api/auth/passkeys/login/begin': 'Login - Passkey - Begin',
		'/api/auth/passkeys/login/finish': 'Login - Passkey - Finish',
		'/api/auth/passkeys/register/begin': 'Passkey - Register - Begin',
		'/api/auth/passkeys/register/finish': 'Passkey - Register - Finish',
		'/api/auth/passkeys/users': 'Passkey',
		'/api/auth/login': 'Login',
		'/api/info/notes': 'Notes',
		'/api/network/switch': 'Standard Switch',
		'/api/vnc': 'VNC',
		'/api/disk/initialize-gpt': 'Disk - Initialize GPT',
		'/api/disk/wipe': 'Disk - Wipe',
		'/api/network/object': 'Network Object',
		'/api/network/dhcp/range': 'DHCP Range',
		'/api/network/dhcp/lease': 'DHCP Lease',
		'/api/system/file-explorer/delete': 'File Explorer - Delete',
		'/api/system/file-explorer': 'File Explorer',
		'/api/system/ppt-devices': 'PCI Passthrough',
		'/api/zfs/datasets/filesystem': 'ZFS Filesystem',
		'/api/zfs/datasets/volume': 'ZFS Volume',
		'/api/samba/shares': 'Samba Share',
		'/api/auth/groups': 'Auth Group',
		'/api/auth/users': 'Auth User',
		'/api/samba/config': 'Samba Config - Edit',
		'/api/zfs/datasets/bulk-delete': 'ZFS Dataset - Bulk Delete',
		'/api/zfs/datasets/snapshot': 'ZFS Snapshot',
		'/api/vm/start': 'VM - Start',
		'/api/vm/stop': 'VM - Stop',
		'/api/vm/shutdown': 'VM - Shutdown',
		'/api/vm/reboot': 'VM - Reboot',
		'/api/vm/description': 'VM - Update Description',
		'/api/jail/action/start': 'Jail - Start',
		'/api/jail/action/stop': 'Jail - Stop',
		'/api/utilities/downloads/signed-url': 'Downloader - Create Signed URL',
		'/api/utilities/download': 'Downloader',
		'/api/vm/storage/detach': 'VM Storage - Detach',
		'/api/vm/storage/attach': 'VM Storage - Attach',
		'/api/vm/network/detach': 'VM Network - Detach',
		'/api/vm/network/attach': 'VM Network - Attach',
		'/api/vm/snapshots/rollback': 'VM Snapshot - Rollback',
		'/api/vm/snapshots': 'VM Snapshot',
		'/api/vm': 'VM',
		'/api/network/manual-switch': 'Manual Switch',
		'/api/zfs/pools': 'ZFS Pool',
		'/api/disk/create-partitions': 'Disk - Create Partitions',
		'/api/jail/snapshots/rollback': 'Jail Snapshot - Rollback',
		'/api/jail/snapshots': 'Jail Snapshot',
		'/api/jail/network/inheritance': 'Jail - Network Inherit',
		'/api/jail/network/disinheritance': 'Jail - Network Disinherit',
		'/api/jail/network': 'Jail Network',
		'/api/jail/description': 'Jail - Update Description',
		'/api/jail/templates/convert': 'Jail Template - Convert',
		'/api/jail/templates/create': 'Jail Template - Create Jail',
		'/api/jail/templates': 'Jail Template',
		'/api/jail': 'Jail',
		'/api/utilities/cloud-init/templates': 'Cloud Init Template',
		'/api/system/basic-settings/pools': 'Basic Settings - ZFS Pools',
		'/api/system/basic-settings/services/dhcp-server/toggle': 'Toggle - DHCP Server',
		'/api/system/basic-settings/services/wol-server/toggle': 'Toggle - WoL Server',
		'/api/system/basic-settings/services/samba-server/toggle': 'Toggle - Samba Server',
		'/api/system/basic-settings/services/jails/toggle': 'Toggle - Jails',
		'/api/system/basic-settings/services/virtualization/toggle': 'Toggle - Virtualization',
		'/api/cluster/notes': 'DC Notes',
		'/api/cluster/reset-node': 'Cluster - Reset Node',
		'/api/cluster/backups/targets/validate': 'DC Backup Target - Validate',
		'/api/cluster/backups/targets': 'DC Backup Target',
		'/api/cluster/backups/jobs/run': 'DC Backup Job - Run',
		'/api/cluster/backups/jobs': 'DC Backup Job',
		'/api/cluster': 'Cluster'
	});

	let records = $derived.by(() => {
		if (!auditRecords.current) return [];

		return auditRecords.current.map((record) => {
			const recordCopy = $state.snapshot(record);
			const path = recordCopy.action?.path || '';
			const method = recordCopy.action?.method || '';

			let resolvedAction = method;

			const matchedEntry = Object.entries(pathToActionMap).find(([prefix]) =>
				path.startsWith(prefix)
			);

			if (matchedEntry) {
				const label = matchedEntry[1];
				if (!label.includes('-')) {
					switch (method.toUpperCase()) {
						case 'GET':
							if (path.includes('vnc')) {
								const port = path.split('/').pop();
								const vm = simpleVmList.current?.find((vm) => vm.vncPort === Number(port));

								resolvedAction = `${label} - ${vm ? vm.name : 'Unknown VM'} (${port})`;
							} else {
								resolvedAction = `${label} - View`;
							}
							break;
						case 'POST':
							resolvedAction = `${label} - Create`;
							break;
						case 'PUT':
						case 'PATCH':
							resolvedAction = `${label} - Update`;
							break;
						case 'DELETE':
							resolvedAction = `${label} - Delete`;
							recordCopy.action.body = {
								id: record.id
							};
							break;
						default:
							resolvedAction = label;
					}
				} else {
					resolvedAction = label;
				}
			}

			if (resolvedAction === 'Login - Create') {
				resolvedAction = 'Login';
			}

			return {
				...recordCopy,
				resolvedAction
			};
		});
	});

	let activeLifecycleCount = $derived(activeLifecycleTasks.current.length);
	let lifecycleActive = $derived(activeLifecycleCount > 0);

	watch(
		() => lifecycleActive,
		(active) => {
			onLifecycleActiveChange?.(active);
		}
	);

	function lifecycleGuestLabel(task: LifecycleTask): string {
		const prefix =
			task.guestType === 'vm'
				? 'VM'
				: task.guestType === 'jail-template'
					? 'Jail Template'
					: 'Jail';
		return `${prefix} ${task.guestId}`;
	}

	export function formatStatus(status: string): string {
		switch (status) {
			case 'started':
				return 'Started';
			case 'success':
				return 'OK';
			case 'client_error':
				return 'Bad Request';
			case 'server_error':
				return 'Error';
			default:
				return status;
		}
	}
</script>

<Tabs.Root value="cluster" class="flex h-full w-full flex-col">
	<Tabs.Content value="cluster" class="flex h-full flex-col border-x border-b">
		<div
			class="relative flex h-full flex-col overflow-hidden"
			transition:fade|global={{ duration: 400 }}
		>
			{#if activeLifecycleCount > 0}
				<div class="bg-muted/35 border-b px-3 py-1.5 text-xs">
					<div class="flex items-center gap-2 overflow-x-auto whitespace-nowrap">
						<span class="inline-flex items-center gap-1 font-medium">
							<span class="icon-[mdi--loading] h-3.5 w-3.5 animate-spin"></span>
							{activeLifecycleCount}
							lifecycle action{activeLifecycleCount === 1 ? '' : 's'} in progress
						</span>

						{#each activeLifecycleTasks.current as task (task.id)}
							<span class="bg-background rounded border px-2 py-0.5">
								{lifecycleGuestLabel(task)}
								{task.action} ({task.status})
							</span>
						{/each}
					</div>
				</div>
			{/if}

			<Table.Root class="w-full table-auto border-collapse">
				<Table.Header class="bg-background sticky top-0 z-50">
					<Table.Row class="dark:hover:bg-background ">
						<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white"
							>Start Time</Table.Head
						>
						<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white"
							>End Time</Table.Head
						>
						<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white">
							{#if clustered && hostnameOptions.length > 0}
								<div class="w-44 max-w-full">
									<SimpleSelect
										placeholder="Node"
										options={hostnameOptions}
										value={effectiveHostname}
										onChange={(value: string) => {
											selectedHostname = value;
										}}
										classes={{
											parent: 'min-w-0 space-y-0',
											trigger:
												'inline-flex h-6 w-full items-center overflow-hidden rounded-sm border-0 bg-transparent px-1.5 text-left text-xs font-medium text-muted-foreground shadow-none ring-0 hover:bg-muted/40 focus:bg-muted/50'
										}}
									/>
								</div>
							{:else}
								Node
							{/if}
						</Table.Head>
						<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white"
							>User</Table.Head
						>
						<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white"
							>Action</Table.Head
						>
						<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white"
							>Status</Table.Head
						>
					</Table.Row>
				</Table.Header>

				<Table.Body class="grow overflow-auto pb-32">
					{#each records as record, i (i)}
						<Table.Row>
							<Table.Cell class="text-wrap px-4 py-2">{convertDbTime(record.started)}</Table.Cell>
							<Table.Cell class="text-wrap px-4 py-2">{convertDbTime(record.ended)}</Table.Cell>
							<Table.Cell class="text-wrap px-4 py-2">{record.node}</Table.Cell>
							<Table.Cell class="text-wrap px-4 py-2"
								>{`${record.user}@${record.authType || 'cluster'}`}</Table.Cell
							>
							<Table.Cell class="text-wrap px-4 py-2" title={JSON.stringify(record.action.body)}
								>{record.resolvedAction}</Table.Cell
							>
							<Table.Cell
								class="text-wrap px-4 py-2"
								title={record.action?.response != null
									? typeof record.action.response === 'string'
										? record.action.response
										: JSON.stringify(record.action.response)
									: 'No response'}
								onclick={() => {
									if (record.action?.response != null && record.action.response) {
										try {
											const data = JSON.stringify(record.action.response);
											navigator.clipboard.writeText(data || '');

											toast.success('Copied response to clipboard', {
												position: 'bottom-center'
											});
										} catch (e) {
											console.log('Error copying resposnse to clipboard', e);
										}
									}
								}}
							>
								{formatStatus(record.status)}
							</Table.Cell>
						</Table.Row>
					{/each}
				</Table.Body>
			</Table.Root>
		</div>
	</Tabs.Content>
</Tabs.Root>
