<script lang="ts">
	import { getAuditRecords } from '$lib/api/info/audit';
	import { getVMs } from '$lib/api/vm/vm';
	import * as Table from '$lib/components/ui/table/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import type { AuditRecord } from '$lib/types/info/audit';
	import type { VM } from '$lib/types/vm/vm';
	import { convertDbTime } from '$lib/utils/time';
	import { createQueries, useQueryClient } from '@tanstack/svelte-query';

	const queryClient = useQueryClient();
	const results = createQueries(() => ({
		queries: [
			{
				queryKey: ['audit-record'],
				queryFn: async () => {
					return await getAuditRecords();
				},
				refetchInterval: false,
				keepPreviousData: true,
				initialData: [] as AuditRecord[]
			},
			{
				queryKey: ['vms-list'],
				queryFn: async () => {
					return await getVMs();
				},
				refetchInterval: false,
				keepPreviousData: true,
				initialData: [] as VM[]
			}
		]
	}));

	$effect(() => {
		if (reload.auditLog) {
			queryClient.refetchQueries({
				queryKey: ['audit-record']
			});
			reload.auditLog = false;
		}
	});

	let data = $derived(results[0].data as AuditRecord[]);
	let vms = $derived(results[1].data as VM[]);

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
		'/api/jail/action/start': 'Jail - Start',
		'/api/jail/action/stop': 'Jail - Stop',
		'/api/utilities/downloads/signed-url': 'Downloader - Create Signed URL',
		'/api/utilities/download': 'Downloader',
		'/api/vm/storage/detach': 'VM Storage - Detach',
		'/api/vm/storage/attach': 'VM Storage - Attach',
		'/api/vm': 'VM',
		'/api/network/manual-switch': 'Manual Switch',
		'/api/zfs/pools': 'ZFS Pool',
		'/api/disk/create-partitions': 'Disk - Create Partitions'
	});

	let records = $derived.by(() => {
		if (!data) return [];

		return data.map((record) => {
			const path = record.action?.path || '';
			const method = record.action?.method || '';

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
								const vm = vms.find((vm) => vm.vncPort === Number(port));

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
							record.action.body = {
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
				...record,
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
		<div class="flex h-full flex-col overflow-hidden">
			<Table.Root class="w-full table-auto border-collapse">
				<Table.Header class="bg-background sticky top-0 z-[50] ">
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

				<Table.Body class="flex-grow overflow-auto pb-32">
					{#each records as record, i (i)}
						<Table.Row>
							<Table.Cell class="text-wrap px-4 py-2">{convertDbTime(record.started)}</Table.Cell>
							<Table.Cell class="text-wrap px-4 py-2">{convertDbTime(record.ended)}</Table.Cell>
							<Table.Cell class="text-wrap px-4 py-2">{record.node}</Table.Cell>
							<Table.Cell class="text-wrap px-4 py-2"
								>{`${record.user}@${record.authType}`}</Table.Cell
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
