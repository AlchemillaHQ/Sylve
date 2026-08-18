<script lang="ts">
	import {
		checkCertificateDomain,
		createCertificate,
		updateCertificate
	} from '$lib/api/services/certificates';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ScrollArea from '$lib/components/ui/scroll-area/scroll-area.svelte';
	import type {
		Certificate,
		CertificateDomainCheck,
		CertificateInput,
		CertificateType
	} from '$lib/types/services/certificates';
	import {
		CERTIFICATE_DOMAIN_MAX_LENGTH,
		CERTIFICATE_NAME_MAX_LENGTH,
		CERTIFICATE_PEM_MAX_BYTES
	} from '$lib/types/services/certificates';
	import type { DynamicDNSEntry } from '$lib/types/services/dynamic-dns';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		hostname: string;
		edit: boolean;
		id?: number;
		certificates: Certificate[];
		dynamicDNSEntries: DynamicDNSEntry[];
		afterChange: () => void;
	}

	let {
		open = $bindable(),
		hostname,
		edit = false,
		id,
		certificates,
		dynamicDNSEntries,
		afterChange
	}: Props = $props();

	const editingCertificate = $derived.by(() =>
		edit && id ? (certificates.find((certificate) => certificate.id === id) ?? null) : null
	);
	const isEditing = $derived(editingCertificate !== null);

	type EditableCertificateType = Exclude<CertificateType, 'system-default'>;
	type Form = {
		name: string;
		type: EditableCertificateType;
		domain: string;
		dynamicDnsEntryId: string;
		staging: boolean;
		validateDomain: boolean;
		certificate: string;
		privateKey: string;
	};

	const typeOptions = [
		{ value: 'imported', label: 'Import PEM' },
		{ value: 'self-signed', label: 'Self-Signed' },
		{ value: 'lets-encrypt', label: "Let's Encrypt (Direct)" },
		{ value: 'sylve-managed', label: 'Sylve.app Managed' }
	];
	const environmentOptions = [
		{ value: 'production', label: 'Production' },
		{ value: 'staging', label: 'Staging' }
	];
	const selectClasses = {
		parent: 'min-w-0 space-y-1.5',
		label: 'flex h-7 items-center whitespace-nowrap text-sm',
		trigger:
			'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 py-1 text-left'
	};
	const managedEntries = $derived(
		dynamicDNSEntries.filter((entry) => entry.provider === 'sylve' && entry.credentialConfigured)
	);
	const managedEntryOptions = $derived(
		managedEntries.map((entry) => ({
			value: String(entry.id),
			label: entry.enabled ? entry.hostname : `${entry.hostname} (disabled)`
		}))
	);
	const domainSuggestions = $derived.by(() => {
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const domains = new Set<string>();
		for (const entry of dynamicDNSEntries) {
			const domain = entry.hostname.trim().toLowerCase().replace(/\.$/, '');
			if (entry.enabled && domain) domains.add(domain);
		}
		return [...domains].sort();
	});

	function defaultForm(): Form {
		return {
			name: '',
			type: 'imported',
			domain: domainSuggestions[0] ?? '',
			dynamicDnsEntryId: '',
			staging: false,
			validateDomain: true,
			certificate: '',
			privateKey: ''
		};
	}

	function certificateForm(certificate: Certificate): Form {
		return {
			name: certificate.name,
			type: certificate.type as EditableCertificateType,
			domain: certificate.domain,
			dynamicDnsEntryId: certificate.dynamicDnsEntryId ? String(certificate.dynamicDnsEntryId) : '',
			staging: certificate.staging,
			validateDomain: certificate.type === 'imported',
			certificate: '',
			privateKey: ''
		};
	}

	// svelte-ignore state_referenced_locally
	let form = $state<Form>(editingCertificate ? certificateForm(editingCertificate) : defaultForm());
	let domainOpen = $state(false);
	let saving = $state(false);
	let checkingDomain = $state(false);
	let domainCheck = $state<CertificateDomainCheck | null>(null);
	let domainCheckFailed = $state(false);
	let domainCheckGeneration = 0;
	let domainCheckController: AbortController | null = null;
	const domainOptions = $derived(
		domainSuggestions.map((domain) => ({ value: domain, label: domain }))
	);
	const domainLabelStatus = $derived.by(() => {
		if (form.type !== 'lets-encrypt') return undefined;
		if (domainCheckFailed) return { text: 'DNS Check Failed', className: 'text-amber-500' };
		if (!domainCheck) return undefined;

		return domainCheck.matches
			? { text: 'DNS Matches This Node', className: 'text-green-500' }
			: { text: 'DNS Does Not Match This Node', className: 'text-amber-500' };
	});

	function clearPEM() {
		form.certificate = '';
		form.privateKey = '';
	}

	function invalidateDomainCheck() {
		domainCheckGeneration += 1;
		domainCheckController?.abort();
		domainCheckController = null;
		checkingDomain = false;
		domainCheck = null;
		domainCheckFailed = false;
	}

	function resetForm() {
		invalidateDomainCheck();
		form = editingCertificate ? certificateForm(editingCertificate) : defaultForm();
	}

	function selectType(value: string) {
		form.type = value as EditableCertificateType;
		invalidateDomainCheck();
		if (form.type !== 'imported') clearPEM();
		if (form.type === 'sylve-managed' && !form.dynamicDnsEntryId && managedEntries[0]) {
			form.dynamicDnsEntryId = String(managedEntries[0].id);
			form.domain = managedEntries[0].hostname;
		} else if (form.type !== 'sylve-managed') {
			form.dynamicDnsEntryId = '';
		}
	}

	function selectManagedEntry(value: string) {
		form.dynamicDnsEntryId = value;
		const entry = managedEntries.find((candidate) => String(candidate.id) === value);
		form.domain = entry?.hostname ?? '';
	}

	async function checkDomain(): Promise<string> {
		const domain = form.domain.trim().toLowerCase().replace(/\.$/, '');
		if (!domain) {
			toast.error('Domain is required', { position: 'bottom-center' });
			return '';
		}

		invalidateDomainCheck();
		const generation = domainCheckGeneration;
		const requestHostname = hostname;
		const controller = new AbortController();
		domainCheckController = controller;
		checkingDomain = true;
		try {
			const result = await checkCertificateDomain(domain, requestHostname, controller.signal);
			if (
				generation !== domainCheckGeneration ||
				!open ||
				form.type !== 'lets-encrypt' ||
				hostname !== requestHostname ||
				form.domain.trim().toLowerCase().replace(/\.$/, '') !== domain
			)
				return '';
			if (isAPIResponse(result)) {
				domainCheckFailed = true;
				handleAPIError(result);
				toast.error('Failed to check domain', { position: 'bottom-center' });
				return '';
			}
			domainCheck = result;
			if (result.matches) {
				toast.success('Domain resolves to this node', { position: 'bottom-center' });
			} else {
				toast.warning('Domain does not resolve to an address detected on this node', {
					position: 'bottom-center'
				});
			}
		} catch (error) {
			if (
				!(error instanceof Error && error.name === 'AbortError') &&
				generation === domainCheckGeneration
			) {
				domainCheckFailed = true;
				toast.error('Failed to check domain', { position: 'bottom-center' });
			}
		} finally {
			if (generation === domainCheckGeneration) {
				checkingDomain = false;
				domainCheckController = null;
			}
		}

		return '';
	}

	async function save() {
		if (saving) return;
		if (edit && !editingCertificate) {
			toast.error('The certificate is no longer available', { position: 'bottom-center' });
			open = false;
			return;
		}

		const name = form.name.trim();
		const domain = form.domain.trim().toLowerCase().replace(/\.$/, '');
		if (!name) {
			toast.error('Name is required', { position: 'bottom-center' });
			return;
		}
		if (name.length > CERTIFICATE_NAME_MAX_LENGTH) {
			toast.error(`Name must not exceed ${CERTIFICATE_NAME_MAX_LENGTH} characters`, {
				position: 'bottom-center'
			});
			return;
		}
		const managedEntry = managedEntries.find(
			(entry) => String(entry.id) === form.dynamicDnsEntryId
		);
		if (form.type === 'sylve-managed' && !managedEntry) {
			toast.error('A Sylve.app Dynamic DNS entry is required', { position: 'bottom-center' });
			return;
		}
		if (!domain) {
			toast.error('Domain is required', { position: 'bottom-center' });
			return;
		}
		if (domain.length > CERTIFICATE_DOMAIN_MAX_LENGTH) {
			toast.error(`Domain must not exceed ${CERTIFICATE_DOMAIN_MAX_LENGTH} characters`, {
				position: 'bottom-center'
			});
			return;
		}
		if (
			form.type === 'lets-encrypt' &&
			(domain.startsWith('*.') || domain.includes(':') || /^\d+(\.\d+){3}$/.test(domain))
		) {
			toast.error("Let's Encrypt requires a non-wildcard DNS hostname", {
				position: 'bottom-center'
			});
			return;
		}
		const certificatePEM = form.certificate.trim();
		const privateKeyPEM = form.privateKey.trim();
		if (
			form.type === 'imported' &&
			(!isEditing || certificatePEM || privateKeyPEM) &&
			(!certificatePEM || !privateKeyPEM)
		) {
			toast.error('Certificate and private key are both required', {
				position: 'bottom-center'
			});
			return;
		}
		if (
			new TextEncoder().encode(certificatePEM).byteLength > CERTIFICATE_PEM_MAX_BYTES ||
			new TextEncoder().encode(privateKeyPEM).byteLength > CERTIFICATE_PEM_MAX_BYTES
		) {
			toast.error('Certificate and private key must not exceed 1 MiB each', {
				position: 'bottom-center'
			});
			return;
		}

		const input: CertificateInput = {
			name,
			type: form.type,
			domain,
			...(form.type === 'sylve-managed' ? { dynamicDnsEntryId: managedEntry!.id } : {}),
			staging: form.type === 'lets-encrypt' && form.staging,
			validateDomain: form.type === 'imported' && form.validateDomain,
			...(form.type === 'imported' && certificatePEM ? { certificate: certificatePEM } : {}),
			...(form.type === 'imported' && privateKeyPEM ? { privateKey: privateKeyPEM } : {})
		};

		const requestHostname = hostname;
		const editingID = editingCertificate?.id;
		const updatingActiveCertificate = Boolean(editingCertificate?.active);
		saving = true;
		try {
			const result = editingID
				? await updateCertificate(editingID, input, requestHostname)
				: await createCertificate(input, requestHostname);
			if (hostname !== requestHostname || !open) return;
			if (isAPIResponse(result)) {
				handleAPIError(result);
				toast.error(`Failed to ${editingID ? 'update' : 'create'} certificate`, {
					position: 'bottom-center'
				});
				return;
			}

			toast.success(
				!editingID && form.type === 'sylve-managed'
					? 'Certificate issuance started'
					: updatingActiveCertificate
						? 'Certificate updated; restart Sylve to apply TLS changes'
						: `Certificate ${editingID ? 'updated' : 'created'}`,
				{ position: 'bottom-center' }
			);
			clearPEM();
			afterChange();
			open = false;
		} finally {
			saving = false;
		}
	}

	function closeForm() {
		invalidateDomainCheck();
		clearPEM();
		open = false;
	}

	let currentHostname = untrack(() => hostname);
	$effect(() => {
		if (hostname !== currentHostname) {
			currentHostname = hostname;
			closeForm();
		}
	});

	onDestroy(() => invalidateDomainCheck());
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-[96%] overflow-hidden p-5 md:max-w-2xl lg:max-w-3xl"
		showCloseButton={true}
		showResetButton={true}
		onReset={resetForm}
		onClose={closeForm}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--certificate-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title={editingCertificate ? `Edit ${editingCertificate.name}` : 'Add Certificate'}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<ScrollArea orientation="vertical" class="h-112 max-h-[62vh] pr-2">
			<div class="space-y-5">
				<section class="grid grid-cols-1 gap-4 sm:grid-cols-2">
					<CustomValueInput
						label="Name"
						placeholder="Home Dashboard"
						bind:value={form.name}
						classes="space-y-1.5"
					/>
					<SimpleSelect
						label="Type"
						options={typeOptions}
						classes={selectClasses}
						bind:value={form.type}
						onChange={selectType}
						disabled={isEditing}
					/>
					{#if form.type === 'sylve-managed'}
						<div class="space-y-1.5 sm:col-span-2">
							<SimpleSelect
								label="Sylve.app Dynamic DNS Entry"
								options={managedEntryOptions}
								placeholder="Select a Sylve.app hostname"
								classes={selectClasses}
								bind:value={form.dynamicDnsEntryId}
								onChange={selectManagedEntry}
								disabled={isEditing}
							/>
						</div>
					{:else}
						<div class={form.type === 'lets-encrypt' ? 'space-y-1.5' : 'space-y-1.5 sm:col-span-2'}>
							<ComboBox
								bind:open={domainOpen}
								label="Domain"
								labelStatus={domainLabelStatus}
								bind:value={form.domain}
								data={domainOptions}
								placeholder="sylve.example.com"
								width="w-full"
								classes="space-y-1.5"
								buttonClass="h-9 px-3 py-1"
								allowCustom={true}
								disallowEmpty={true}
								onValueChange={() => {
									invalidateDomainCheck();
								}}
								topRightButton={form.type === 'lets-encrypt'
									? {
											icon: checkingDomain
												? 'icon-[mdi--loading] animate-spin'
												: 'icon-[mdi--radar]',
											tooltip: checkingDomain ? 'Checking DNS mapping...' : 'Check DNS mapping',
											function: checkDomain
										}
									: undefined}
							/>
							{#if form.type === 'lets-encrypt' && domainCheck?.warning}
								<p class="text-muted-foreground text-xs">{domainCheck.warning}</p>
							{/if}
						</div>
					{/if}
					{#if form.type === 'lets-encrypt'}
						<SimpleSelect
							label="Environment"
							options={environmentOptions}
							classes={selectClasses}
							value={form.staging ? 'staging' : 'production'}
							onChange={(value) => (form.staging = value === 'staging')}
						/>
					{/if}
				</section>

				{#if form.type === 'imported'}
					<div class="space-y-4">
						<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
							<CustomValueInput
								label="Certificate PEM"
								placeholder={isEditing
									? 'Leave blank to keep the stored certificate'
									: '-----BEGIN CERTIFICATE-----'}
								bind:value={form.certificate}
								type="textarea"
								textAreaClasses="min-h-36 font-mono text-xs"
							/>
							<CustomValueInput
								label="Private Key PEM"
								placeholder={isEditing
									? 'Leave blank to keep the stored private key'
									: '-----BEGIN PRIVATE KEY-----'}
								bind:value={form.privateKey}
								type="textarea"
								textAreaClasses="min-h-36 font-mono text-xs"
							/>
						</div>
						<CustomCheckbox
							label="Validate certificate against domain"
							bind:checked={form.validateDomain}
						/>
					</div>
				{:else if form.type === 'lets-encrypt'}
					<p class="rounded-md border bg-muted/20 p-3 text-xs text-muted-foreground">
						TLS-ALPN-01 requires direct public access on TCP port 443; wildcards are unsupported.
					</p>
				{:else if form.type === 'sylve-managed'}
					<p class="rounded-md border bg-muted/20 p-3 text-xs text-muted-foreground">
						Sylve will create a private P-256 key locally and request the certificate through
						Sylve.app. Issuance continues in the background and does not activate the certificate.
					</p>
				{:else if form.type === 'self-signed'}
					<p class="rounded-md border bg-muted/20 p-3 text-xs text-muted-foreground">
						Sylve will generate a random P-256 certificate for this domain. This does not affect the
						internal cluster certificate.
					</p>
				{/if}
			</div>
		</ScrollArea>

		<Dialog.Footer class="pt-2">
			<Button size="sm" onclick={save} disabled={saving}>
				{saving ? 'Saving...' : isEditing ? 'Save' : 'Create'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
