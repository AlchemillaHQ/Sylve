<script lang="ts">
	import { saveNFSConfig } from '$lib/api/nfs';
	import { getKerberosKeytabs } from '$lib/api/directory-services';
	import { getInterfaces } from '$lib/api/network/iface';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { IfaceSchema, type Iface } from '$lib/types/network/iface';
	import { NFSMutationSchema, NFSValidationErrorSchema, type NFSConfig } from '$lib/types/nfs';
	import { APIRequestError, cachedFetch, handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { Address4, Address6 } from 'ip-address';
	import { onDestroy, onMount, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	let {
		open = $bindable(),
		node,
		config,
		reload = $bindable(false)
	}: {
		open: boolean;
		node: string;
		config: NFSConfig;
		reload?: boolean;
	} = $props();
	const targetNode = untrack(() => node);
	const initial = untrack(() => ({ ...config }));
	let protocol = $state<string>(initial.protocol);
	let v4MaxVersion = $state<string>(initial.v4MaxVersion);
	let numericIds = $state(initial.numericIds);
	let udp = $state(initial.udp);
	let allowNonroot = $state(initial.allowNonroot);
	let bindAddresses = $state<string | string[]>(initial.bindAddresses.slice());
	let bindOpen = $state(false);
	let interfaces = $state<Iface[]>([]);
	let interfacesLoading = $state(true);
	let interfacesError = $state('');
	let bindOptions = $derived.by(() => {
		const options: Record<string, { value: string; label: string }> = {};
		for (const iface of interfaces) {
			const description = iface.description.trim();
			const name = description ? `${description} (${iface.name})` : iface.name;
			for (const { ip } of [...(iface.ipv4 ?? []), ...(iface.ipv6 ?? [])]) {
				if (ip.includes('/') || ip.includes('%')) continue;
				try {
					const address = ip.includes(':') ? new Address6(ip) : new Address4(ip);
					if (
						address.isUnspecified() ||
						address.isMulticast() ||
						address.isLinkLocal() ||
						address.isBroadcast() ||
						(address instanceof Address6 && address.isMapped4())
					)
						continue;
					const value = address.correctForm();
					options[value] ??= { value, label: `${name} — ${value}` };
				} catch {
					continue;
				}
			}
		}
		return Object.values(options).sort((a, b) => a.label.localeCompare(b.label));
	});
	let mountdPort = $state<string | number>(initial.mountdPort || '');
	let minThreads = $state<string | number>(initial.minThreads || '');
	let maxThreads = $state<string | number>(initial.maxThreads || '');
	let kerberosPrincipal = $state<string | string[]>(initial.kerberosPrincipal);
	let principalOpen = $state(false);
	let principals = $state<string[]>([]);
	let principalsLoading = $state(true);
	let principalError = $state('');
	let principalOptions = $derived.by(() => {
		const options = principals.map((value) => ({ value, label: value, disabled: false }));
		if (initial.kerberosPrincipal && !principals.includes(initial.kerberosPrincipal)) {
			options.push({
				value: initial.kerberosPrincipal,
				label: initial.kerberosPrincipal,
				disabled: true
			});
		}
		return options;
	});
	let saving = $state(false);
	let error = $state('');
	const selectClasses = {
		parent: 'min-w-0 space-y-1.5',
		label: 'flex h-7 items-center whitespace-nowrap text-sm',
		trigger:
			'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 py-1 text-left'
	};
	let mounted = true;
	onDestroy(() => {
		mounted = false;
	});
	onMount(() => {
		const controller = new AbortController();
		void cachedFetch(
			'network-interfaces',
			async () => {
				const result = await getInterfaces({ hostname: targetNode, signal: controller.signal });
				if (isAPIResponse(result)) throw new APIRequestError(result);
				return result;
			},
			60_000,
			false,
			targetNode,
			IfaceSchema.array()
		)
			.then((result) => {
				if (mounted) interfaces = result;
			})
			.catch(() => {
				if (mounted) interfacesError = 'Unable to load interface addresses.';
			})
			.finally(() => {
				if (mounted) interfacesLoading = false;
			});
		void getKerberosKeytabs({ hostname: targetNode, signal: controller.signal })
			.then((keytabs) => {
				if (!mounted) return;
				principals = [...new Set(keytabs.flatMap((keytab) => keytab.principals))]
					.filter((principal) => principal.startsWith('nfs/'))
					.sort();
			})
			.catch(() => {
				if (mounted) principalError = 'Unable to load Directory Services keytabs.';
			})
			.finally(() => {
				if (mounted) principalsLoading = false;
			});
		return () => controller.abort();
	});
	function reset() {
		protocol = initial.protocol;
		v4MaxVersion = initial.v4MaxVersion;
		numericIds = initial.numericIds;
		udp = initial.udp;
		allowNonroot = initial.allowNonroot;
		bindAddresses = initial.bindAddresses.slice();
		mountdPort = initial.mountdPort || '';
		minThreads = initial.minThreads || '';
		maxThreads = initial.maxThreads || '';
		kerberosPrincipal = initial.kerberosPrincipal;
		error = '';
	}
	async function save() {
		if (saving) return;
		saving = true;
		error = '';
		try {
			const result = await saveNFSConfig(
				{
					protocol: protocol as NFSConfig['protocol'],
					v4MaxVersion: v4MaxVersion as NFSConfig['v4MaxVersion'],
					numericIds,
					udp,
					allowNonroot,
					bindAddresses: (Array.isArray(bindAddresses) ? bindAddresses : [bindAddresses])
						.map((value) => value.trim())
						.filter(Boolean),
					mountdPort: Number(mountdPort),
					minThreads: Number(minThreads),
					maxThreads: Number(maxThreads),
					kerberosPrincipal: typeof kerberosPrincipal === 'string' ? kerberosPrincipal.trim() : ''
				},
				{ hostname: targetNode }
			);
			if (!mounted) return;
			if (result.status === 'error') {
				const detail = NFSValidationErrorSchema.safeParse(result.data);
				if (detail.success) error = detail.data.message;
				handleAPIError(result);
				return;
			}
			const mutation = NFSMutationSchema.safeParse(result.data);
			if (mutation.success && mutation.data.applyStatus === 'stopped') {
				toast.success('NFS settings saved', { position: 'bottom-center' });
			} else if (mutation.success && mutation.data.applied) {
				toast.success('NFS settings applied', { position: 'bottom-center' });
			} else {
				toast.warning('NFS settings saved; apply pending.', {
					position: 'bottom-center'
				});
			}
			reload = true;
			open = false;
		} finally {
			if (mounted) saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex max-h-[90dvh] flex-col overflow-hidden sm:max-w-2xl"
		showCloseButton={!saving}
		showResetButton={!saving}
		onReset={reset}
		onClose={() => {
			if (!saving) open = false;
		}}
		onEscapeKeydown={(event) => {
			if (saving) event.preventDefault();
		}}
		onInteractOutside={(event) => {
			if (saving) event.preventDefault();
		}}
	>
		<Dialog.Header class="shrink-0 pr-10">
			<Dialog.Title
				><SpanWithIcon
					icon="icon-[mdi--folder-settings-variant]"
					size="h-5 w-5"
					gap="gap-2"
					title="NFS Settings"
				/></Dialog.Title
			>
		</Dialog.Header>
		{#if error}<p
				class="border-destructive/40 bg-destructive/10 text-destructive rounded-md border p-3 text-sm"
				role="alert"
			>
				{error}
			</p>{/if}
		<div class="flex min-h-0 flex-col gap-6 overflow-y-auto">
			<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
				<SimpleSelect
					label="Protocol"
					bind:value={protocol}
					classes={selectClasses}
					options={[
						{ value: 'v3', label: 'NFSv3' },
						{ value: 'v4', label: 'NFSv4' },
						{ value: 'v3v4', label: 'NFSv3 and NFSv4' }
					]}
					onChange={() => {
						if (protocol === 'v4') udp = false;
					}}
					disabled={saving}
				/>
				<SimpleSelect
					label="Maximum NFSv4 Version"
					onChange={() => {}}
					bind:value={v4MaxVersion}
					classes={selectClasses}
					options={[
						{ value: '4.0', label: 'NFSv4.0' },
						{ value: '4.1', label: 'NFSv4.1' },
						{ value: '4.2', label: 'NFSv4.2' }
					]}
					disabled={saving || protocol === 'v3'}
				/>
				<ComboBox
					label="Bind Addresses"
					placeholder="All Interfaces"
					hint="Select interface addresses or enter a local IPv4/IPv6 address. Empty means all interfaces."
					multiple
					allowCustom
					showCount
					bind:open={bindOpen}
					bind:value={bindAddresses}
					data={bindOptions}
					loading={interfacesLoading}
					searchError={interfacesError}
					classes="space-y-1.5"
					triggerWidth="flex w-full"
					buttonClass="h-9 min-h-9 py-1"
					width="w-full"
					disabled={saving}
				/>
				<CustomValueInput
					label="Mountd Port"
					placeholder="Automatic"
					type="number"
					bind:value={mountdPort}
					disabled={saving}
				/>
				<CustomValueInput
					label="Minimum Threads"
					placeholder="Automatic"
					type="number"
					bind:value={minThreads}
					disabled={saving}
				/>
				<CustomValueInput
					label="Maximum Threads"
					placeholder="Automatic"
					type="number"
					bind:value={maxThreads}
					disabled={saving}
				/>
				<ComboBox
					label="Kerberos Principal"
					bind:open={principalOpen}
					bind:value={kerberosPrincipal}
					data={principalOptions}
					placeholder="None"
					loading={principalsLoading}
					searchError={principalError}
					classes="space-y-1.5 sm:col-span-2"
					triggerWidth="flex w-full"
					buttonClass="h-9 min-h-9 py-1"
					width="w-full"
					disabled={saving}
				/>
			</div>
			<div class="grid grid-cols-1 gap-2 sm:grid-cols-[repeat(3,max-content)] sm:gap-x-4">
				<CustomCheckbox
					label="Numeric IDs (NFSv4)"
					bind:checked={numericIds}
					disabled={saving || protocol === 'v3'}
				/>
				<CustomCheckbox
					label="UDP (NFSv3)"
					bind:checked={udp}
					disabled={saving || protocol === 'v4'}
				/>
				<CustomCheckbox
					label="Allow Unprivileged Ports"
					bind:checked={allowNonroot}
					disabled={saving}
				/>
			</div>
		</div>
		<Dialog.Footer class="shrink-0"
			><Button size="sm" disabled={saving} onclick={save}
				>{saving ? 'Saving...' : 'Save Changes'}</Button
			></Dialog.Footer
		>
	</Dialog.Content>
</Dialog.Root>
