<script lang="ts">
	import { storage } from '$lib';
	import {
		getActiveDirectory,
		getADStatus,
		getKerberosRealms,
		getKerberosKeytabs,
		checkADStatus
	} from '$lib/api/directory-services';
	import Editor from '$lib/components/custom/DirectoryServices/ActiveDirectory.svelte';
	import Membership from '$lib/components/custom/DirectoryServices/Membership.svelte';
	import Recovery from '$lib/components/custom/DirectoryServices/Recovery.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type {
		ActiveDirectory,
		ADStatus,
		KerberosRealm,
		KerberosKeytab
	} from '$lib/types/directory-services';
	import { convertDbTime } from '$lib/utils/time';
	import { formatDirectoryServiceError } from '$lib/utils/directory-services';
	import { renderWithIcon } from '$lib/utils/table';
	import { escapeHTML } from '$lib/utils/string';
	import type { CellComponent } from 'tabulator-tables';
	import { useInterval, watch } from 'runed';
	import { onDestroy, untrack } from 'svelte';

	let {
		data
	}: {
		data: {
			node: string;
			ad: ActiveDirectory | null;
			status: ADStatus | null;
			realms: KerberosRealm[];
			keytabs: KerberosKeytab[];
			loadFailed: boolean;
		};
	} = $props();
	const initial = untrack(() => data);
	let ad = $state<ActiveDirectory | null>(initial.ad);
	let status = $state<ADStatus | null>(initial.status);
	let realms = $state(initial.realms);
	let keytabs = $state(initial.keytabs);
	let loadFailed = $state(initial.loadFailed);
	let busy = $state(false);
	let refreshing = $state(false);
	let polling = $state(false);
	let editorOpen = $state(false);
	let membershipOpen = $state(false);
	let recoveryOpen = $state(false);
	let leave = $state(false);
	let reload = $state(false);
	let query = $state('');
	let generation = 0;
	onDestroy(() => {
		generation++;
	});
	let operation = $derived(status?.state === 'JOINING' || status?.state === 'LEAVING');
	let pendingLeave = $derived(
		ad?.membershipPending === 'leave' || ad?.membershipPending === 'leave-cleanup'
	);
	let disabled = $derived(busy || refreshing || operation || loadFailed);
	// @wc-ignore
	let warning = $derived(
		(status?.warning ?? '')
			.replace(
				'No alternate domain controller was verified. Domain-controller redundancy is unverified.',
				''
			)
			.trim() ||
			ad?.joinWarning ||
			''
	);
	watch.pre(
		() => data,
		(next) => {
			generation++;
			busy = false;
			refreshing = false;
			polling = false;
			reload = false;
			ad = next.ad;
			status = next.status;
			realms = next.realms;
			keytabs = next.keytabs;
			loadFailed = next.loadFailed;
			editorOpen = false;
			membershipOpen = false;
			recoveryOpen = false;
			query = '';
		}
	);
	async function refresh(background = false) {
		if (refreshing || polling) return;
		if (background) polling = true;
		else refreshing = true;
		const node = data.node;
		const requestGeneration = generation;
		const canApply = () =>
			generation === requestGeneration &&
			(!background || (!busy && !reload && !editorOpen && !membershipOpen && !recoveryOpen));
		try {
			const result = await Promise.all([
				getActiveDirectory({ hostname: node }),
				getADStatus({ hostname: node }),
				getKerberosRealms({ hostname: node }),
				getKerberosKeytabs({ hostname: node })
			]);
			if (!canApply()) return;
			[ad, status, realms, keytabs] = result;
			loadFailed = false;
		} catch {
			if (canApply()) loadFailed = true;
		} finally {
			if (generation === requestGeneration) {
				if (background) polling = false;
				else refreshing = false;
			}
		}
	}
	async function check() {
		if (disabled) return;
		busy = true;
		const node = data.node;
		const requestGeneration = generation;
		try {
			const result = await checkADStatus({ hostname: node });
			if (generation !== requestGeneration) return;
			status = result;
			reload = true;
		} catch {
			if (generation === requestGeneration) {
				loadFailed = true;
				reload = true;
			}
		} finally {
			if (generation === requestGeneration) busy = false;
		}
	}

	watch(
		[() => reload, () => busy, () => refreshing, () => polling],
		([requested, busy, refreshing, polling]) => {
			if (requested && !busy && !refreshing && !polling) {
				reload = false;
				void refresh();
			}
		}
	);

	useInterval(3000, {
		callback: () => {
			if (
				storage.visible &&
				!busy &&
				!refreshing &&
				!polling &&
				!editorOpen &&
				!membershipOpen &&
				!recoveryOpen
			)
				void refresh(true);
		}
	});

	let table = $derived({
		columns: [
			{ title: 'Property', field: 'property' },
			{
				title: 'Value',
				field: 'value',
				formatter: (cell: CellComponent) => {
					if (cell.getRow().getData().id === 'state') {
						const healthy = status?.state === 'HEALTHY';
						const faulted = status?.state === 'FAULTED';
						return renderWithIcon(
							healthy ? 'mdi:check-circle' : faulted ? 'mdi:alert-circle' : 'mdi:circle-outline',
							cell.getValue(),
							healthy ? 'text-green-500' : faulted ? 'text-red-500' : 'text-muted-foreground'
						);
					}
					return escapeHTML(String(cell.getValue()));
				}
			}
		],
		rows: ad
			? [
					{
						id: 'state',
						property: 'Connection Status',
						value: status?.state.replaceAll('_', ' ') ?? 'Unknown'
					},
					{
						id: 'joined',
						property: 'Domain Membership',
						value: ad.joined ? 'Joined' : 'Not Joined'
					},
					{ id: 'enabled', property: 'Enabled', value: ad.enabled ? 'Yes' : 'No' },
					{ id: 'domain', property: 'Domain', value: ad.domain || 'Not Configured' },
					{ id: 'workgroup', property: 'Workgroup', value: ad.workgroup || '-' },
					{ id: 'computer', property: 'Computer Name', value: ad.computerName },
					{
						id: 'controller',
						property: 'Domain Controller',
						value: status?.domainController || 'Not Checked'
					},
					{ id: 'site', property: 'AD Site', value: ad.site || status?.site || 'Automatic' },
					{
						id: 'alternate',
						property: 'Alternate Domain Controller',
						value: status?.alternateVerified
							? `${status.alternateController} (Verified)`
							: 'Not Verified'
					},
					{
						id: 'dns-registration',
						property: 'AD DNS Registration',
						value: ad.skipDNSRegistration ? 'Skipped on Join' : 'Register on Join'
					},
					{
						id: 'realm',
						property: 'Kerberos Realm',
						value: realms.find((realm) => realm.id === ad?.realmId)?.realm || 'Automatic'
					},
					{
						id: 'principal',
						property: 'Join Authentication',
						value: ad.keytabId
							? ad.kerberosPrincipal
							: ad.joinPasswordConfigured
								? `${ad.joinUsername} (Saved Credentials)`
								: 'Username and Password'
					},
					{
						id: 'mapping',
						property: 'Identity Mapping',
						value: `${ad.idmapBackend.toUpperCase()} · ${ad.rangeLow}–${ad.rangeHigh}`
					},
					{
						id: 'trust',
						property: 'Trusted Domains',
						value: ad.allowTrustedDomains ? 'Allowed' : 'Disabled'
					},
					{
						id: 'checked',
						property: 'Last Health Check',
						value:
							status?.checkedAt && !status.checkedAt.startsWith('0001')
								? convertDbTime(status.checkedAt)
								: 'Not Checked'
					}
				]
			: []
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			size="sm"
			class="h-6"
			disabled={!ad || disabled || !!ad.membershipPending || status?.recoveryPending}
			onclick={() => (editorOpen = true)}
			><SpanWithIcon
				icon="icon-[hugeicons--system-update-01]"
				size="h-4 w-4"
				gap="gap-2"
				title="Update"
			/></Button
		>
		{#if ad?.joined || ad?.membershipPending}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={disabled || !ad.enabled || status?.recoveryPending}
				onclick={check}
			>
				<SpanWithIcon
					icon="icon-[mdi--connection]"
					size="h-4 w-4"
					gap="gap-2"
					title="Check Connection"
				/>
			</Button>
		{/if}
		<Button
			size="sm"
			variant="outline"
			class="h-6.5"
			disabled={!ad?.domain || disabled || status?.recoveryPending}
			onclick={() => {
				leave = (!!ad?.joined && !ad.membershipPending) || pendingLeave;
				membershipOpen = true;
			}}
		>
			<SpanWithIcon
				icon={ad?.membershipPending
					? 'icon-[mdi--reload]'
					: ad?.joined
						? 'icon-[mdi--lan-disconnect]'
						: 'icon-[mdi--lan-connect]'}
				size="h-4 w-4"
				gap="gap-2"
				title={ad?.membershipPending === 'join'
					? 'Retry Join'
					: ad?.membershipPending === 'leave-cleanup'
						? 'Retry Cleanup'
						: ad?.joined || pendingLeave
							? 'Leave Domain'
							: 'Join Domain'}
			/>
		</Button>
		{#if ad?.membershipPending === 'join'}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={disabled || status?.recoveryPending}
				onclick={() => {
					leave = true;
					membershipOpen = true;
				}}
			>
				<SpanWithIcon
					icon="icon-[mdi--lan-disconnect]"
					size="h-4 w-4"
					gap="gap-2"
					title="Remove Membership"
				/>
			</Button>
		{/if}
		<Button
			size="sm"
			variant="outline"
			class="h-6.5"
			disabled={disabled || (!status?.recoveryPending && (!ad?.joined || !!ad.membershipPending))}
			onclick={() => (recoveryOpen = true)}
		>
			<SpanWithIcon icon="icon-[mdi--backup-restore]" size="h-4 w-4" gap="gap-2" title="Recovery" />
		</Button>
	</div>
	{#if loadFailed}<div
			class="m-3 flex items-center gap-2 rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-sm text-amber-700 dark:text-amber-300"
			role="alert"
		>
			<span class="icon-[mdi--alert-circle-outline] size-4 shrink-0"></span><span
				>Unable to refresh directory services. {ad
					? 'Displaying the last known configuration.'
					: 'Configuration is unavailable.'} Changes are disabled until the connection recovers.</span
			>
		</div>{/if}
	{#if status?.error}<div
			class="m-3 flex items-center gap-2 rounded-md border border-red-500/30 bg-red-500/5 px-3 py-2 text-sm text-red-700 dark:text-red-300"
			role="alert"
		>
			<span class="icon-[mdi--alert-circle-outline] size-4 shrink-0"></span><span
				>{formatDirectoryServiceError(status.error)}</span
			>
		</div>{/if}
	{#if warning}<div
			class="m-3 flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-sm text-amber-700 dark:text-amber-300"
			role="status"
		>
			<span class="icon-[mdi--alert-circle-outline] mt-0.5 size-4 shrink-0"></span><span
				>{warning}</span
			>
		</div>{/if}
	<TreeTable
		data={table}
		name="directory-ad-tt"
		multipleSelect={false}
		borderTop={!!(loadFailed || status?.error || warning)}
		bind:query
	/>
</div>
{#if ad && editorOpen}<Editor
		bind:open={editorOpen}
		node={data.node}
		{ad}
		{realms}
		{keytabs}
		bind:reload
	/>{/if}
{#if ad && recoveryOpen}<Recovery bind:open={recoveryOpen} node={data.node} {ad} bind:reload />{/if}
{#if ad && membershipOpen}<Membership
		bind:open={membershipOpen}
		node={data.node}
		{ad}
		{leave}
		bind:reload
	/>{/if}
