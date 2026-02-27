<script lang="ts">
	import { getAuditRecords } from '$lib/api/info/audit';
	import { getSimpleVMs } from '$lib/api/vm/vm';
	import * as Table from '$lib/components/ui/table/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import { updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import { fade } from 'svelte/transition';

	const auditRecords = resource(
		() => 'audit-record',
		async (key, prevKey, { signal }) => {
			const results = await getAuditRecords();
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

	const pathToActionMap: Record<string, string> = $derived({
		'/api/auth/login': 'Login',
		'/api/info/notes': 'Notes',
		'/api/network/switch': 'Standard Switch',
		'/api/vnc': 'VNC',
		'/api/disk/initialize-gpt': 'Disk - Initialize GPT',
		'/api/disk/wipe': 'Disk - Wipe',
		'/api/network/object': 'Network Object',
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
		'/api/vm': 'VM',
		'/api/network/manual-switch': 'Manual Switch',
		'/api/zfs/pools': 'ZFS Pool',
		'/api/disk/create-partitions': 'Disk - Create Partitions',
		'/api/jail/network/inheritance': 'Jail - Network Inherit',
		'/api/jail/network/disinheritance': 'Jail - Network Disinherit',
		'/api/jail/network': 'Jail Network',
		'/api/jail': 'Jail',
		'/api/utilities/cloud-init/templates': 'Cloud Init Template',
		'/api/system/basic-settings/services/dhcp-server/toggle': 'Toggle - DHCP Server',
		'/api/system/basic-settings/services/wol-server/toggle': 'Toggle - WoL Server',
		'/api/system/basic-settings/services/samba-server/toggle': 'Toggle - Samba Server',
		'/api/system/basic-settings/services/jails/toggle': 'Toggle - Jails',
		'/api/system/basic-settings/services/virtualization/toggle': 'Toggle - Virtualization',
		'/api/cluster/notes': 'Cluster Notes',
		'/api/cluster/reset-node': 'Cluster - Reset Node',
		'/api/cluster/backups/targets/validate': 'Cluster Backup Target - Validate',
		'/api/cluster/backups/targets': 'Cluster Backup Target',
		'/api/cluster/backups/jobs': 'Cluster Backup Job',
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
		<div class="flex h-full flex-col overflow-hidden" transition:fade|global={{ duration: 400 }}>
			<Table.Root class="w-full table-auto border-collapse">
				<Table.Header class="bg-background sticky top-0 z-50">
					<Table.Row class="dark:hover:bg-background ">
						<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white"
							>Start Time</Table.Head
						>
						<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white"
							>End Time</Table.Head
						>
						<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white"
							>Node</Table.Head
						>
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
