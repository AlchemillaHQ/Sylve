<script lang="ts">
	import {
		activateCertificate,
		cancelCertificateActivation,
		deleteCertificate,
		downloadCertificate,
		getCertificates,
		renewCertificate,
		retryCertificateIssuance
	} from '$lib/api/services/certificates';
	import { getDynamicDNSEntries } from '$lib/api/services/dynamic-dns';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import Form from '$lib/components/custom/Services/Certificates/Form.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import { reload } from '$lib/stores/api.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { Certificate } from '$lib/types/services/certificates';
	import type { DynamicDNSEntry } from '$lib/types/services/dynamic-dns';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { renderWithIcon } from '$lib/utils/table';
	import { convertDbTime } from '$lib/utils/time';
	import { IsDocumentVisible, resource, useInterval } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		hostname: string;
		certificates: Certificate[];
		dynamicDNSEntries: DynamicDNSEntry[];
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	let certificateSnapshot = data.certificates;
	// svelte-ignore state_referenced_locally
	const certificateResource = resource(
		() => data.hostname,
		async (hostname, _previousHostname, { signal }) => {
			const result = await getCertificates(hostname, signal);
			if (Array.isArray(result)) {
				certificateSnapshot = result;
				await updateCache('certificates', result, hostname);
				return result;
			}
			return certificateSnapshot;
		},
		{ initialValue: data.certificates }
	);
	const certificates = $derived(
		Array.isArray(certificateResource.current)
			? (certificateResource.current as Certificate[])
			: []
	);

	// svelte-ignore state_referenced_locally
	let dynamicDNSSnapshot = data.dynamicDNSEntries;
	// svelte-ignore state_referenced_locally
	const dynamicDNSResource = resource(
		() => data.hostname,
		async (hostname, _previousHostname, { signal }) => {
			const result = await getDynamicDNSEntries(hostname, signal, true);
			if (Array.isArray(result)) {
				dynamicDNSSnapshot = result;
				await updateCache('dynamic-dns-entries', result, hostname);
				return result;
			}
			return dynamicDNSSnapshot;
		},
		{ initialValue: data.dynamicDNSEntries }
	);
	const dynamicDNSEntries = $derived(
		Array.isArray(dynamicDNSResource.current)
			? (dynamicDNSResource.current as DynamicDNSEntry[])
			: []
	);

	let activeRow = $state<Row[] | null>(null);
	let query = $state('');
	let activating = $state(false);
	let cancellingActivation = $state(false);
	let renewing = $state(false);
	let retrying = $state(false);
	let downloading = $state(false);
	let deleting = $state(false);
	let modals = $state({
		create: { open: false },
		edit: { open: false, id: 0 },
		delete: { open: false }
	});
	let currentHostname = data.hostname;
	let pageGeneration = 0;
	$effect(() => {
		if (data.hostname === currentHostname) return;
		currentHostname = data.hostname;
		pageGeneration += 1;
		certificateSnapshot = data.certificates;
		dynamicDNSSnapshot = data.dynamicDNSEntries;
		certificateResource.mutate(data.certificates);
		dynamicDNSResource.mutate(data.dynamicDNSEntries);
		activeRow = null;
		query = '';
		activating = false;
		cancellingActivation = false;
		renewing = false;
		retrying = false;
		downloading = false;
		deleting = false;
		modals.create.open = false;
		modals.edit = { open: false, id: 0 };
		modals.delete.open = false;
	});
	const activeIssuanceStatuses = new Set<Certificate['issuanceStatus']>([
		'submitting',
		'queued',
		'processing',
		'blocked'
	]);

	const selectedCertificate = $derived.by(() => {
		const selectedRow = activeRow?.[0];
		if (!selectedRow || activeRow?.length !== 1) return null;
		return certificates.find((certificate) => certificate.id === Number(selectedRow.id)) ?? null;
	});
	const issuancePending = $derived(
		certificates.some((certificate) => activeIssuanceStatuses.has(certificate.issuanceStatus))
	);
	const visible = new IsDocumentVisible();
	let now = $state(Date.now());
	useInterval(5000, {
		callback: () => {
			if (visible.current && issuancePending) {
				void certificateResource.refetch();
			}
		}
	});
	useInterval(60_000, { callback: () => (now = Date.now()) });

	const htmlEscapes: Record<string, string> = {
		'&': '&amp;',
		'<': '&lt;',
		'>': '&gt;',
		"'": '&#39;',
		'"': '&quot;'
	};
	type StatusDisplay = [string, string, string];
	type ValidityStatus =
		| 'valid'
		| 'not-yet-valid'
		| 'expired'
		| 'expiring'
		| 'invalid'
		| 'unavailable';
	const initialIssuanceStates: Partial<
		Record<Certificate['issuanceStatus'], StatusDisplay>
	> = {
		submitting: ['mdi:key-plus', 'Preparing Request', 'text-blue-400'],
		queued: ['mdi:timer-sand', 'Issuance Queued', 'text-blue-400'],
		processing: ['mdi:progress-key', 'Issuing', 'text-blue-400'],
		blocked: ['mdi:account-clock-outline', 'Waiting for Active Order', 'text-amber-500'],
		failed: ['mdi:alert-circle', 'Issuance Failed', 'text-red-500']
	};
	const renewalIssuanceStates: Partial<
		Record<Certificate['issuanceStatus'], StatusDisplay>
	> = {
		submitting: ['mdi:autorenew', 'Renewal Preparing', 'text-blue-400'],
		queued: ['mdi:autorenew', 'Renewal Queued', 'text-blue-400'],
		processing: ['mdi:autorenew', 'Renewing', 'text-blue-400'],
		blocked: ['mdi:account-clock-outline', 'Renewal Waiting', 'text-amber-500'],
		failed: ['mdi:alert-circle', 'Renewal Failed', 'text-red-500']
	};

	function escapeHTML(value: unknown): string {
		return String(value ?? '').replace(/[&<>'"]/g, (character) => htmlEscapes[character] ?? character);
	}

	function certificateValidity(certificate: Certificate): ValidityStatus {
		if (!certificate.ready || !certificate.notBefore || !certificate.notAfter) return 'unavailable';
		const startsAt = new Date(certificate.notBefore).getTime();
		const expiresAt = new Date(certificate.notAfter).getTime();
		if (!Number.isFinite(startsAt) || !Number.isFinite(expiresAt) || expiresAt <= startsAt) {
			return 'invalid';
		}
		if (startsAt > now) return 'not-yet-valid';
		if (expiresAt <= now) return 'expired';
		if (expiresAt - now <= 30 * 86_400_000) return 'expiring';
		return 'valid';
	}

	function formatStatus(cell: CellComponent): string {
		const row = cell.getRow().getData() as {
			active: boolean;
			pending: boolean;
			ready: boolean;
			validity: ValidityStatus;
			issuanceStatus: Certificate['issuanceStatus'];
			issuanceOperation: Certificate['issuanceOperation'];
			issuanceError: string;
			issuanceRetryAt: string | null | undefined;
		};
		const values: string[] = [];
		if (!row.ready) {
			const pendingState = initialIssuanceStates[row.issuanceStatus];
			values.push(
				pendingState
					? renderWithIcon(pendingState[0], pendingState[1], pendingState[2])
					: renderWithIcon('mdi:alert-circle', 'Unavailable', 'text-red-500')
			);
		} else {
			values.push(
				row.active
				? renderWithIcon('mdi:check-decagram', 'Active', 'text-green-500')
				: row.pending
					? renderWithIcon('mdi:restart-alert', 'Pending Restart', 'text-amber-500')
					: renderWithIcon('mdi:certificate-outline', 'Available', 'text-muted-foreground')
			);
			if (row.issuanceOperation === 'renewal') {
				const renewalState = renewalIssuanceStates[row.issuanceStatus];
				if (renewalState) {
					values.push(renderWithIcon(renewalState[0], renewalState[1], renewalState[2]));
				}
			}
		}
		if (row.issuanceRetryAt && activeIssuanceStatuses.has(row.issuanceStatus)) {
			values.push(
				renderWithIcon(
					'mdi:clock-outline',
					`Next check ${escapeHTML(convertDbTime(row.issuanceRetryAt))}`,
					'text-muted-foreground'
				)
			);
		}
		switch (row.validity) {
			case 'not-yet-valid':
				values.push(renderWithIcon('mdi:clock-alert-outline', 'Not Yet Valid', 'text-red-500'));
				break;
			case 'expired':
				values.push(renderWithIcon('mdi:alert-circle', 'Expired', 'text-red-500'));
				break;
			case 'expiring':
				values.push(renderWithIcon('mdi:clock-alert-outline', 'Expiring', 'text-amber-500'));
				break;
			case 'invalid':
				values.push(renderWithIcon('mdi:alert-octagon', 'Invalid Dates', 'text-red-500'));
				break;
		}
		const details = [
			row.issuanceError,
			row.issuanceRetryAt ? `Next broker check: ${convertDbTime(row.issuanceRetryAt)}` : ''
		].filter(Boolean);
		const title = details.length > 0 ? ` title="${escapeHTML(details.join(' | '))}"` : '';
		return `<div class="flex flex-col gap-1"${title}>${values.join('')}</div>`;
	}

	function formatType(cell: CellComponent): string {
		switch (cell.getValue()) {
			case 'imported':
				return renderWithIcon('mdi:file-key-outline', 'Imported', 'text-blue-400');
			case 'self-signed':
				return renderWithIcon('mdi:signature-freehand', 'Self-Signed', 'text-violet-400');
			case 'lets-encrypt': {
				const staging = Boolean(cell.getRow().getData().staging);
				return renderWithIcon(
					'mdi:lock-check-outline',
					staging ? "Let's Encrypt (Direct, Staging)" : "Let's Encrypt (Direct)",
					staging ? 'text-amber-400' : 'text-green-500'
				);
			}
			case 'sylve-managed':
				return renderWithIcon('mdi:cloud-lock-outline', 'Sylve.app Managed', 'text-cyan-400');
			default:
				return renderWithIcon('mdi:shield-home-outline', 'System Default', 'text-slate-400');
		}
	}

	function refetchCertificates() {
		void certificateResource.refetch();
	}

	function refreshData() {
		void certificateResource.refetch();
		void dynamicDNSResource.refetch();
	}

	async function activateSelected() {
		const certificate = selectedCertificate;
		if (!certificate || !certificate.ready || certificate.active || certificate.pending || activating) return;
		const requestHostname = data.hostname;
		const generation = pageGeneration;
		activating = true;
		try {
			const result = await activateCertificate(certificate.id, requestHostname);
			if (data.hostname !== requestHostname || generation !== pageGeneration) return;
			if (isAPIResponse(result)) {
				handleAPIError(result);
				toast.error(`Failed to activate ${certificate.name}`, { position: 'bottom-center' });
				return;
			}
			toast.success(`${certificate.name} will become active after Sylve restarts`, {
				position: 'bottom-center'
			});
			await certificateResource.refetch();
		} finally {
			if (generation === pageGeneration) activating = false;
		}
	}

	async function cancelPendingActivation() {
		const certificate = selectedCertificate;
		if (!certificate || !certificate.pending || cancellingActivation) return;
		const requestHostname = data.hostname;
		const generation = pageGeneration;
		cancellingActivation = true;
		try {
			const result = await cancelCertificateActivation(certificate.id, requestHostname);
			if (data.hostname !== requestHostname || generation !== pageGeneration) return;
			if (result.status !== 'success') {
				handleAPIError(result);
				toast.error(`Failed to cancel activation for ${certificate.name}`, {
					position: 'bottom-center'
				});
				return;
			}
			toast.success(`Pending activation cancelled for ${certificate.name}`, {
				position: 'bottom-center'
			});
			await certificateResource.refetch();
		} finally {
			if (generation === pageGeneration) cancellingActivation = false;
		}
	}

	async function renewSelected() {
		const certificate = selectedCertificate;
		if (
			!certificate ||
			!certificate.renewable ||
			(certificate.type !== 'lets-encrypt' && certificate.type !== 'sylve-managed') ||
			renewing
		)
			return;
		const requestHostname = data.hostname;
		const generation = pageGeneration;
		renewing = true;
		try {
			const result = await renewCertificate(certificate.id, requestHostname);
			if (data.hostname !== requestHostname || generation !== pageGeneration) return;
			if (isAPIResponse(result)) {
				handleAPIError(result);
				toast.error(`Failed to renew ${certificate.name}`, { position: 'bottom-center' });
				return;
			}
			toast.success(
				certificate.type === 'sylve-managed'
					? `${certificate.name} renewal started`
					: `${certificate.name} renewed`,
				{ position: 'bottom-center' }
			);
			await certificateResource.refetch();
		} finally {
			if (generation === pageGeneration) renewing = false;
		}
	}

	async function retrySelected() {
		const certificate = selectedCertificate;
		if (
			!certificate ||
			certificate.type !== 'sylve-managed' ||
			certificate.ready ||
			certificate.issuanceStatus !== 'failed' ||
			retrying
		)
			return;
		const requestHostname = data.hostname;
		const generation = pageGeneration;
		retrying = true;
		try {
			const result = await retryCertificateIssuance(certificate.id, requestHostname);
			if (data.hostname !== requestHostname || generation !== pageGeneration) return;
			if (isAPIResponse(result)) {
				handleAPIError(result);
				toast.error(`Failed to retry issuance for ${certificate.name}`, {
					position: 'bottom-center'
				});
				return;
			}
			toast.success(`Certificate issuance restarted for ${certificate.name}`, {
				position: 'bottom-center'
			});
			await certificateResource.refetch();
		} finally {
			if (generation === pageGeneration) retrying = false;
		}
	}

	async function downloadSelected() {
		const certificate = selectedCertificate;
		if (!certificate?.ready || downloading) return;

		const requestHostname = data.hostname;
		const generation = pageGeneration;
		downloading = true;
		try {
			const archive = await downloadCertificate(certificate.id, requestHostname);
			if (data.hostname !== requestHostname || generation !== pageGeneration) return;
			if (isAPIResponse(archive)) {
				handleAPIError(archive);
				const message = Array.isArray(archive.error)
					? archive.error.join('; ')
					: archive.error || archive.message || 'Certificate download failed';
				throw new Error(message);
			}
			if (!(archive instanceof Blob) || archive.size === 0 || !archive.type.includes('zip')) {
				throw new Error('The server did not return a valid certificate archive');
			}

			let link: HTMLAnchorElement | null = null;
			const url = URL.createObjectURL(archive);
			try {
				link = Object.assign(document.createElement('a'), {
					href: url,
					download: `sylve-certificate-${certificate.id}.zip`,
					style: 'display:none'
				});
				document.body.appendChild(link);
				link.click();
			} finally {
				link?.remove();
				URL.revokeObjectURL(url);
			}
			toast.success(`Downloaded certificate and private key for ${certificate.name}`, {
				position: 'bottom-center'
			});
		} catch {
			toast.error(`Failed to download certificate and private key for ${certificate.name}`, {
				position: 'bottom-center'
			});
		} finally {
			if (generation === pageGeneration) downloading = false;
			reload.auditLog = true;
		}
	}

	async function confirmDelete() {
		const certificate = selectedCertificate;
		if (!certificate || deleting) return;
		const requestHostname = data.hostname;
		const generation = pageGeneration;
		deleting = true;
		try {
			const result = await deleteCertificate(certificate.id, requestHostname);
			if (data.hostname !== requestHostname || generation !== pageGeneration) return;
			if (result.status !== 'success') {
				handleAPIError(result);
				toast.error(`Failed to delete ${certificate.name}`, { position: 'bottom-center' });
				return;
			}
			toast.success('Certificate deleted', { position: 'bottom-center' });
			activeRow = null;
			modals.delete.open = false;
			await certificateResource.refetch();
		} finally {
			if (generation === pageGeneration) deleting = false;
		}
	}

	const columns: Column[] = $derived([
		{ field: 'id', title: 'ID', visible: false },
		{ field: 'displayStatus', title: 'Status', formatter: formatStatus },
		{
			field: 'name',
			title: 'Name',
			formatter: (cell: CellComponent) =>
				renderWithIcon('mdi:certificate-outline', escapeHTML(cell.getValue()), 'text-indigo-400')
		},
		{ field: 'type', title: 'Type', formatter: formatType },
		{
			field: 'domain',
			title: 'Domain',
			formatter: (cell: CellComponent) => escapeHTML(cell.getValue()),
			copyOnClick: true
		},
		{
			field: 'expires',
			title: 'Expires',
			formatter: (cell: CellComponent) =>
				cell.getValue()
					? convertDbTime(cell.getValue())
					: '<span class="text-muted-foreground">Pending issuance</span>'
		},
		{
			field: 'fingerprint',
			title: 'SHA-256 Fingerprint',
			formatter: (cell: CellComponent) =>
				cell.getValue()
					? `<span class="font-mono text-xs">${escapeHTML(cell.getValue())}</span>`
					: '<span class="text-muted-foreground">&mdash;</span>',
			copyOnClick: (row) => Boolean(row.getData().fingerprint)
		},
		{
			field: 'updatedAt',
			title: 'Updated',
			formatter: (cell: CellComponent) => convertDbTime(cell.getValue())
		}
	]);

	const tableData = $derived({
		columns,
		rows: certificates.map((certificate) => {
			const validity = certificateValidity(certificate);
			const displayStatus = !certificate.ready
				? certificate.issuanceStatus === 'ready'
					? 'unavailable'
					: certificate.issuanceStatus
				: validity === 'expired' || validity === 'not-yet-valid' || validity === 'invalid'
					? validity
					: certificate.active
						? 'active'
						: certificate.pending
							? 'pending'
							: validity === 'expiring'
								? 'expiring'
								: 'available';
			return {
				id: certificate.id,
				active: certificate.active,
				pending: certificate.pending,
				ready: certificate.ready,
				staging: certificate.staging,
				validity,
				issuanceStatus: certificate.issuanceStatus,
				issuanceOperation: certificate.issuanceOperation,
				issuanceError: certificate.issuanceError,
				issuanceRetryAt: certificate.issuanceRetryAt,
				displayStatus,
				name: certificate.name,
				type: certificate.type,
				domain: certificate.domain,
				expires: certificate.notAfter,
				fingerprint: certificate.fingerprint,
				updatedAt: certificate.updatedAt
			};
		})
	});
</script>

{#snippet selectedActions()}
	{#if selectedCertificate}
		{#if selectedCertificate.ready}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				onclick={downloadSelected}
				disabled={downloading}
			>
				<SpanWithIcon
					icon="icon-[mdi--download]"
					size="h-4 w-4"
					gap="gap-2"
					title={downloading ? 'Downloading...' : 'Download Certificate & Key'}
				/>
			</Button>
		{/if}
		{#if selectedCertificate.ready && !selectedCertificate.active && !selectedCertificate.pending}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				onclick={activateSelected}
				disabled={activating}
			>
				<SpanWithIcon
					icon="icon-[mdi--restart]"
					size="h-4 w-4"
					gap="gap-2"
					title={activating ? 'Scheduling...' : 'Activate on Restart'}
				/>
			</Button>
		{/if}
		{#if selectedCertificate.pending}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				onclick={cancelPendingActivation}
				disabled={cancellingActivation}
			>
				<SpanWithIcon
					icon="icon-[mdi--restart-off]"
					size="h-4 w-4"
					gap="gap-2"
					title={cancellingActivation ? 'Cancelling...' : 'Cancel Pending'}
				/>
			</Button>
		{/if}
		{#if selectedCertificate.type === 'sylve-managed' && !selectedCertificate.ready && selectedCertificate.issuanceStatus === 'failed'}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				onclick={retrySelected}
				disabled={retrying}
			>
				<SpanWithIcon
					icon="icon-[mdi--reload-alert]"
					size="h-4 w-4"
					gap="gap-2"
					title={retrying ? 'Retrying...' : 'Retry Issuance'}
				/>
			</Button>
		{/if}
		{#if (selectedCertificate.type === 'lets-encrypt' || selectedCertificate.type === 'sylve-managed') && selectedCertificate.renewable}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				onclick={renewSelected}
				disabled={renewing}
			>
				<SpanWithIcon
					icon="icon-[mdi--autorenew]"
					size="h-4 w-4"
					gap="gap-2"
					title={renewing ? 'Renewing...' : 'Renew'}
				/>
			</Button>
		{/if}
		{#if selectedCertificate.type !== 'system-default'}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				onclick={() => {
					modals.edit.id = selectedCertificate.id;
					modals.edit.open = true;
				}}
			>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
			</Button>
		{/if}
		{#if selectedCertificate.type !== 'system-default' && !selectedCertificate.active && !selectedCertificate.pending}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				onclick={() => (modals.delete.open = true)}
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button size="sm" class="h-6" onclick={() => (modals.create.open = true)}>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>
		{@render selectedActions()}
		<Button size="sm" variant="outline" class="ml-auto h-6" title="Refresh" onclick={refreshData}>
			<span class="icon-[mdi--refresh] h-4 w-4"></span>
		</Button>
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={tableData}
			name="tt-certificates"
			bind:parentActiveRow={activeRow}
			bind:query
			multipleSelect={false}
			dataTree={false}
		/>
	</div>
</div>

{#if modals.create.open}
	<Form
		bind:open={modals.create.open}
		hostname={data.hostname}
		{certificates}
		{dynamicDNSEntries}
		edit={false}
		afterChange={refetchCertificates}
	/>
{/if}

{#if modals.edit.open}
	<Form
		bind:open={modals.edit.open}
		hostname={data.hostname}
		{certificates}
		{dynamicDNSEntries}
		edit={true}
		id={modals.edit.id}
		afterChange={refetchCertificates}
	/>
{/if}

<AlertDialog
	open={modals.delete.open}
	names={{ parent: 'certificate', element: selectedCertificate?.name ?? '' }}
	loading={deleting}
	actions={{
		onConfirm: () => void confirmDelete(),
		onCancel: () => {
			modals.delete.open = false;
		}
	}}
/>
