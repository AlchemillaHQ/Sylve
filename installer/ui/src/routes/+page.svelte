<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { slide } from 'svelte/transition';
	import { cubicOut } from 'svelte/easing';
	import { prefersReducedMotion } from 'svelte/motion';
	import { watch } from 'runed';
	import { cn } from '$lib/utils';
	import AboutDialog from '$lib/components/AboutDialog.svelte';
	import Combobox from '$lib/components/Combobox.svelte';
	import DiskKindIcon from '$lib/components/DiskKindIcon.svelte';
	import DiskManager from '$lib/components/DiskManager.svelte';
	import LiveNetworkDialog from '$lib/components/LiveNetworkDialog.svelte';
	import * as Dialog from '$lib/components/ui/dialog';
	import PartitionModeChoice from '$lib/components/PartitionModeChoice.svelte';
	import PoolDialog from '$lib/components/PoolDialog.svelte';
	import SadCat from '$lib/components/SadCat.svelte';
	import TimezoneMap from '$lib/components/TimezoneMap.svelte';
	import { LICENSE_TEXT } from '$lib/license';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Progress } from '$lib/components/ui/progress';
	import {
		configDisks,
		defaultPool,
		diskLabel,
		formatBytes,
		formatSize,
		isPartitionMember,
		MIN_TARGET_BYTES,
		parseSwapSize,
		poolLayoutLabel,
		poolReady,
		shortDevice,
		suggestUfsSwap,
		toPoolDisk,
		usableBytes,
		ufsRootBytes,
		vdevSummary,
		type ApiDisk,
		type PartitioningMode,
		type PoolConfig,
		type PoolDisk
	} from '$lib/pool';
	import { normalizeSizeInput } from '$lib/size';
	import Icon from '@iconify/svelte';
	import partitionOutlined from '@iconify-icons/ant-design/partition-outlined';
	import Check from '@lucide/svelte/icons/check';
	import CircleAlert from '@lucide/svelte/icons/circle-alert';
	import ArrowRight from '@lucide/svelte/icons/arrow-right';
	import Eye from '@lucide/svelte/icons/eye';
	import EyeOff from '@lucide/svelte/icons/eye-off';
	import Globe from '@lucide/svelte/icons/globe';
	import HardDrive from '@lucide/svelte/icons/hard-drive';
	import Heart from '@lucide/svelte/icons/heart';
	import ListChecks from '@lucide/svelte/icons/list-checks';
	import KeyRound from '@lucide/svelte/icons/key-round';
	import LockKeyhole from '@lucide/svelte/icons/lock-keyhole';
	import LoaderCircle from '@lucide/svelte/icons/loader-circle';
	import Info from '@lucide/svelte/icons/info';
	import Moon from '@lucide/svelte/icons/moon';
	import Pencil from '@lucide/svelte/icons/pencil';
	import Power from '@lucide/svelte/icons/power';
	import QrCode from '@lucide/svelte/icons/qr-code';
	import RefreshCw from '@lucide/svelte/icons/refresh-cw';
	import RotateCw from '@lucide/svelte/icons/rotate-cw';
	import ScrollText from '@lucide/svelte/icons/scroll-text';
	import Sun from '@lucide/svelte/icons/sun';
	import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
	import Wrench from '@lucide/svelte/icons/wrench';
	import X from '@lucide/svelte/icons/x';

	type StepId =
		| 'checks'
		| 'hostname'
		| 'disk'
		| 'password'
		| 'network'
		| 'summary'
		| 'progress'
		| 'done';

	type Iface = { name: string; ipv4: string[] | null };
	type SystemInfo = {
		hostname: string;
		kernel: string;
		bootMethod: string;
		mode: string;
		cpus: number;
		interfaces: Iface[];
	};
	type InstallStep = { id: string; label: string; detail: string; state: string };
	type AccessState = { authenticated: boolean; kiosk: boolean; password?: string };

	const FLOW: { id: StepId; label: string; hint: string }[] = [
		{ id: 'checks', label: 'Checks', hint: 'Machine checks before anything is written.' },
		{
			id: 'hostname',
			label: 'Hostname, Timezone and Keyboard',
			hint: 'The name this system answers to, plus its console keyboard and local time.'
		},
		{
			id: 'disk',
			label: 'Storage',
			hint: 'Choose Auto (ZFS) or Auto (UFS).'
		},
		{
			id: 'password',
			label: 'Passwords',
			hint: 'Root access and the Sylve administrator account.'
		},
		{ id: 'network', label: 'Network', hint: 'How the installed system joins the network.' },
		{ id: 'summary', label: 'Summary', hint: 'Review the plan before the install starts.' }
	];

	const timezones = [
		'UTC',
		'Europe/London',
		'Europe/Berlin',
		'Europe/Amsterdam',
		'Asia/Kolkata',
		'Asia/Singapore',
		'Asia/Tokyo',
		'America/New_York',
		'America/Chicago',
		'America/Los_Angeles',
		'Australia/Sydney'
	];
	const keymaps = ['us', 'uk', 'de', 'fr', 'es', 'it', 'in', 'jp'];

	const KEYMAP_LABELS: Record<string, string> = {
		us: 'English (US)',
		uk: 'English (UK)',
		'uk.capslock': 'English (UK, caps lock)',
		de: 'German',
		'de.noacc': 'German (no accents)',
		'de.acc': 'German (accents)',
		fr: 'French',
		'fr.acc': 'French (accents)',
		'fr.noacc': 'French (no accents)',
		es: 'Spanish',
		it: 'Italian',
		pt: 'Portuguese',
		br: 'Portuguese (Brazil)',
		'br.noacc': 'Portuguese (Brazil, no accents)',
		nl: 'Dutch',
		se: 'Swedish',
		no: 'Norwegian',
		dk: 'Danish',
		fi: 'Finnish',
		is: 'Icelandic',
		pl: 'Polish',
		cz: 'Czech',
		sk: 'Slovak',
		hu: 'Hungarian',
		ro: 'Romanian',
		ru: 'Russian',
		ua: 'Ukrainian',
		bg: 'Bulgarian',
		'bg.phonetic': 'Bulgarian (phonetic)',
		'bg.bds': 'Bulgarian (BDS)',
		gr: 'Greek',
		tr: 'Turkish',
		il: 'Hebrew',
		ar: 'Arabic',
		in: 'English (India)',
		jp: 'Japanese',
		kr: 'Korean',
		cn: 'Chinese',
		ca: 'Canadian',
		'ca-fr': 'Canadian (French)',
		'ca-multi': 'Canadian (multilingual)',
		be: 'Belgian',
		'be.acc': 'Belgian (accents)',
		ch: 'Swiss',
		'ch.de': 'Swiss (German)',
		'ch.fr': 'Swiss (French)',
		am: 'Armenian',
		by: 'Belarusian',
		kz: 'Kazakh',
		th: 'Thai',
		vn: 'Vietnamese'
	};

	function prettyTZ(tz: string) {
		const parts = tz.split('/');
		const city = (parts.pop() ?? '').replaceAll('_', ' ');
		let region = parts.join('/').replaceAll('_', ' ');
		if (region === 'Etc') region = 'ETC';
		return region ? `${city} · ${region}` : city;
	}

	function prettyKeymap(km: string) {
		return KEYMAP_LABELS[km] ?? km.replaceAll('.', ' ');
	}

	let step = $state<StepId>('checks');
	let system = $state<SystemInfo | null>(null);
	let dark = $state(false);
	let access = $state<AccessState | null>(null);
	let accessPassword = $state('');
	let accessError = $state('');
	let accessBusy = $state(false);
	let accessPasswordVisible = $state(false);
	const ACCESS_MIN_MS = 700;
	const ACCESS_LOAD_MIN_MS = 1000;

	async function loadAccess(password?: string) {
		const res = await fetch('/api/access', password === undefined ? {} : {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ password })
		});
		const data = await res.json();
		if (res.status === 401) throw new Error('Incorrect password. Try again.');
		if (!res.ok) throw new Error(data?.error ?? `HTTP ${res.status}`);
		return data as AccessState;
	}

	async function initializeAccess() {
		const started = Date.now();
		const token = new URLSearchParams(location.hash.slice(1)).get('kiosk');
		if (token) history.replaceState(null, '', location.pathname + location.search);
		try {
			const session = await loadAccess(token ?? undefined);
			if (session.authenticated) await loadInstaller();
			await minWait(started, ACCESS_LOAD_MIN_MS);
			access = session;
		} catch (err) {
			await minWait(started, ACCESS_LOAD_MIN_MS);
			accessError = err instanceof Error ? err.message : String(err);
			access = { authenticated: false, kiosk: false };
		}
	}

	async function authenticate() {
		if (accessBusy || access === null) return;
		const started = Date.now();
		accessBusy = true;
		accessError = '';
		try {
			const session = await loadAccess(accessPassword);
			if (session.authenticated) await loadInstaller();
			await minWait(started, ACCESS_MIN_MS);
			access = session;
			accessPassword = '';
			accessPasswordVisible = false;
		} catch (err) {
			await minWait(started, ACCESS_MIN_MS);
			accessError = err instanceof Error ? err.message : String(err);
		} finally {
			accessBusy = false;
		}
	}

	let hostname = $state('sylve');
	let timezone = $state('UTC');
	let keymap = $state('us');
	let tzList = $state<string[]>(timezones);
	let kmList = $state<string[]>(keymaps);
	const tzOptions = $derived(tzList.map((tz) => ({ value: tz, label: prettyTZ(tz) })));
	const kmOptions = $derived(kmList.map((km) => ({ value: km, label: prettyKeymap(km) })));

	type PreflightCheck = {
		id: string;
		label: string;
		detail: string;
		error?: string;
		status: 'ok' | 'warn' | 'fail';
		fatal: boolean;
	};
	let preflight = $state<PreflightCheck[]>([]);
	let preflightRunning = $state(false);
	let preflightError = $state('');
	let licenseAccepted = $state(false);
	let licenseDeclined = $state(false);
	let liveNetworkOpen = $state(false);
	let qrOpen = $state(false);
	let qrText = $state('');

	function enlargeQR(text: string) {
		qrText = text;
		qrOpen = true;
	}

	async function liveNetworkApplied() {
		liveNetworkOpen = false;
		try {
			system = await (await fetch('/api/system')).json();
			if (!uplink) uplink = system?.interfaces?.[0]?.name ?? '';
		} catch {
		}
		await runChecks();
	}

	function agreeLicense() {
		licenseDeclined = false;
		licenseAccepted = true;
	}

	const CHECK_MIN_MS = 1000;
	let checksRun = 0;
	let checkSkeletonCount = $state(6);

	async function runChecks() {
		const run = ++checksRun;
		preflightRunning = true;
		preflightError = '';
		preflight = [];
		const started = Date.now();
		try {
			const res = await fetch('/api/preflight');
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			const data = await res.json();
			await minWait(started);
			if (run !== checksRun) return;
			preflight = Array.isArray(data.checks) ? data.checks : [];
			checkSkeletonCount = preflight.length || checkSkeletonCount;
		} catch (err) {
			await minWait(started);
			if (run !== checksRun) return;
			preflightError = err instanceof Error ? err.message : String(err);
		} finally {
			if (run === checksRun) preflightRunning = false;
		}
	}

	function minWait(started: number, minimum = CHECK_MIN_MS) {
		const remaining = minimum - (Date.now() - started);
		return remaining > 0 ? new Promise((resolve) => setTimeout(resolve, remaining)) : Promise.resolve();
	}

	let rawDisks = $state<ApiDisk[]>([]);
	let manageOpen = $state(false);

	let partitionMode = $state<PartitioningMode>('zfs');
	let zfsSwap = $state('2 GiB');
	let ufsDisk = $state('');
	let ufsTarget = $state<'entire' | 'free'>('entire');
	let ufsSwap = $state('');
	let pool = $state<PoolConfig>(defaultPool());
	let poolDialogOpen = $state(false);
	let encrypt = $state(false);
	let passphrase = $state('');
	let passphrase2 = $state('');

	let rootPassword = $state('');
	let rootPassword2 = $state('');
	let adminPassword = $state('');

	let uplink = $state('');
	let networkMode = $state<'dhcp' | 'static'>('dhcp');
	let staticAddress = $state('');
	let staticGateway = $state('');
	let staticDNS = $state('1.1.1.1');
	let networkMode6 = $state<'off' | 'slaac' | 'static'>('slaac');
	let staticAddress6 = $state('');
	let staticGateway6 = $state('');
	let staticDNS6 = $state('');

	let installSteps = $state<InstallStep[]>([]);
	let installLogs = $state<string[]>([]);
	let installPercent = $state(0);
	let installError = $state('');
	let installRunning = $state(false);
	let installSocket: WebSocket | undefined;
	let installPoller: ReturnType<typeof setInterval> | undefined;
	const MAX_LOG_LINES = 400;

	async function reloadDisks() {
		try {
			rawDisks = await (await fetch('/api/disks')).json();
		} catch {
		}
	}

	watch(
		() => current.id,
		(id) => {
			if (id === 'disk') reloadDisks();
		}
	);

	watch(
		[() => current.id, () => licenseAccepted],
		([id, accepted]) => {
			if (id === 'checks' && accepted) runChecks();
		}
	);

	function toggleTheme() {
		dark = !dark;
		document.documentElement.classList.toggle('dark', dark);
		localStorage.setItem('theme', dark ? 'dark' : 'light');
	}

	const hostnameOK = $derived(
		/^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$/.test(
			hostname
		)
	);
	const stepIndex = $derived(FLOW.findIndex((s) => s.id === step));
	const current = $derived(FLOW[stepIndex] ?? FLOW[0]);

	const allDisks = $derived<PoolDisk[]>(
		rawDisks.map(toPoolDisk).sort((a, b) => a.device.localeCompare(b.device))
	);
	const poolDisks = $derived(allDisks.filter((disk) => !disk.boot));
	const usable = $derived(
		usableBytes(poolDisks, pool, {
			swapBytes: parseSwapSize(zfsSwap) ?? 0,
			encrypted: encrypt
		})
	);
	const poolMembers = $derived(configDisks(pool));

	const ufsDiskInfo = $derived(poolDisks.find((disk) => disk.device === ufsDisk) ?? null);
	const ufsHasScheme = $derived(ufsDiskInfo?.scheme === 'GPT');
	const ufsFreeBytes = $derived(ufsDiskInfo ? ufsRootBytes(ufsDiskInfo, 'free', 0, false) : 0);
	const ufsSwapSuggestion = $derived(
		ufsDiskInfo ? suggestUfsSwap(ufsTarget === 'free' ? ufsFreeBytes : ufsDiskInfo.sizeBytes) : 0
	);
	const ufsSwapLabel = $derived.by(() => {
		const bytes = parseSwapSize(ufsSwap);
		if (bytes === null) return ufsSwap.trim() || 'none';
		return bytes === 0 ? 'none' : formatBytes(bytes);
	});
	const ufsRootEstimate = $derived.by(() => {
		if (!ufsDiskInfo) return 0;
		return ufsRootBytes(ufsDiskInfo, ufsTarget, parseSwapSize(ufsSwap) ?? 0, encrypt);
	});
	function selectUfsDisk(device: string) {
		ufsDisk = device;
		const disk = poolDisks.find((item) => item.device === device);
		if (disk) {
			const available = ufsTarget === 'free' ? ufsRootBytes(disk, 'free', 0, false) : disk.sizeBytes;
			ufsSwap = formatSize(suggestUfsSwap(available));
		}
	}

	function selectUfsTarget(target: 'entire' | 'free') {
		ufsTarget = target;
		selectUfsDisk(ufsDisk);
	}

	function selectMode(mode: PartitioningMode) {
		if (mode === 'ufs') selectUfsDisk(ufsDisk || poolDisks[0]?.device || '');
	}

	const zfsSwapInvalid = $derived(zfsSwap.trim() !== '' && parseSwapSize(zfsSwap) === null);
	const ufsSwapInvalid = $derived(ufsSwap.trim() !== '' && parseSwapSize(ufsSwap) === null);

	const targetBytes = $derived(partitionMode === 'ufs' ? ufsRootEstimate : usable);
	const targetTooSmall = $derived(
		(partitionMode === 'ufs' ? !!ufsDisk : poolMembers.length > 0) && targetBytes < MIN_TARGET_BYTES
	);
	const diskValid = $derived.by(() => {
		const passOK = !encrypt || (passphrase.length >= 8 && passphrase === passphrase2);
		if (partitionMode === 'ufs') {
			return (
				ufsDisk !== '' && parseSwapSize(ufsSwap) !== null && passOK && targetBytes >= MIN_TARGET_BYTES
			);
		}
		return poolReady(pool) && parseSwapSize(zfsSwap) !== null && passOK && targetBytes >= MIN_TARGET_BYTES;
	});
	const adminPasswordValid = $derived(Array.from(adminPassword).length >= 8);
	const passwordValid = $derived(
		rootPassword.length > 0 && rootPassword === rootPassword2 && adminPasswordValid
	);
	const networkValid = $derived.by(() => {
		if ((system?.interfaces ?? []).length === 0) return true;
		if (uplink === '') return false;
		if (networkMode === 'static' && (staticAddress.length === 0 || staticGateway.length === 0)) {
			return false;
		}
		if (networkMode6 === 'static' && staticAddress6.length === 0) return false;
		return true;
	});
	const canNext = $derived.by(() => {
		switch (step) {
			case 'checks':
				return (
					licenseAccepted &&
					!preflightRunning &&
					preflightError === '' &&
					preflight.length > 0 &&
					!preflight.some((c) => c.status === 'fail')
				);
			case 'hostname':
				return hostnameOK;
			case 'disk':
				return diskValid;
			case 'password':
				return passwordValid;
			case 'network':
				return networkValid;
			default:
				return true;
		}
	});

	const lanHost = $derived(
		system?.interfaces?.find((iface) => iface.name === uplink)?.ipv4?.[0] ??
			system?.interfaces?.find((iface) => (iface.ipv4 ?? []).length > 0)?.ipv4?.[0] ??
			''
	);

	const webURL = $derived(lanHost ? `http://${lanHost}:8080` : '');
	const sylveURL = $derived(lanHost ? `https://${lanHost}:8181` : '');

	function next() {
		if (stepIndex < FLOW.length - 1) {
			step = FLOW[stepIndex + 1].id;
		} else {
			beginInstall();
		}
	}
	function back() {
		if (step === 'checks') {
			if (licenseAccepted) {
				licenseAccepted = false;
				licenseDeclined = false;
			}
			return;
		}
		if (stepIndex > 0) step = FLOW[stepIndex - 1].id;
	}

	function review(): { label: string; value: string; step: StepId; wide?: boolean }[] {
		return [
			{ label: 'Hostname', value: hostname, step: 'hostname' },
			{
				label: 'Keyboard and timezone',
				value: `${prettyKeymap(keymap)} · ${prettyTZ(timezone)}`,
				step: 'hostname'
			},
			{
				label: 'Storage',
				value: storageSummary(),
				step: 'disk',
				wide: true
			},
			{
				label: 'Encryption',
				value: encrypt ? 'GELI (AES-XTS-256)' : 'off',
				step: 'disk'
			},
			{
				label: 'Accounts',
				value:
					rootPassword && adminPassword
						? 'root + Sylve admin passwords set'
						: 'passwords not set',
				step: 'password'
			},
			{
				label: 'Network',
				value:
					(system?.interfaces ?? []).length === 0
						? 'no interfaces detected — switch created without a port'
						: `${networkMode === 'dhcp' ? 'DHCP' : `${staticAddress} · gateway ${staticGateway}`} · ${
								networkMode6 === 'off'
									? 'IPv6 off'
									: networkMode6 === 'slaac'
										? 'IPv6 SLAAC'
										: `IPv6 ${staticAddress6}`
							}${uplink ? ` · ${uplink}` : ''}`,
				step: 'network',
				wide: true
			}
		];
	}

	function storageSummary(): string {
		if (partitionMode === 'ufs') {
			return `ufs · ${shortDevice(ufsDisk || 'ada0')} · ${ufsTarget === 'entire' ? 'entire disk' : 'free space'} · swap ${ufsSwap.trim() || '0'}${encrypt ? ' · GELI' : ''}`;
		}
		return `zfs · ${pool.name} · ${vdevSummary(pool)} · swap ${zfsSwap.trim() || '0'}${encrypt ? ' · GELI' : ''}`;
	}

	function buildPlan() {
		const network = {
			mode: networkMode,
			uplink,
			address: networkMode === 'static' ? staticAddress.trim() : '',
			gateway: networkMode === 'static' ? staticGateway.trim() : '',
			dns: networkMode === 'static' ? staticDNS.trim() : '',
			mode6: networkMode6,
			address6: networkMode6 === 'static' ? staticAddress6.trim() : '',
			gateway6: networkMode6 === 'static' ? staticGateway6.trim() : '',
			dns6: networkMode6 === 'static' ? staticDNS6.trim() : ''
		};
		const storage =
			partitionMode === 'ufs'
				? {
						mode: 'ufs',
						ufs: {
							disk: shortDevice(ufsDisk),
							target: ufsTarget,
							swap: ufsSwap.trim() || '0',
							encrypt,
							passphrase: encrypt ? passphrase : ''
						}
					}
				: {
						mode: 'zfs',
						zfs: {
							name: pool.name,
							vdevs: pool.vdevs.map((vdev) => ({
								type: vdev.raid,
								disks: vdev.disks.map(shortDevice)
							})),
							encrypt,
							passphrase: encrypt ? passphrase : '',
							swap: zfsSwap.trim() || '0',
							compression: pool.compression,
							ashift: pool.ashift,
							autotrim: pool.autotrim,
							comment: pool.comment.trim(),
							properties: Object.fromEntries(
								pool.properties
									.map((prop) => [prop.key.trim(), prop.value.trim()] as const)
									.filter(([key]) => key !== '')
							)
						}
					};
		return {
			hostname: hostname.trim(),
			timezone,
			keymap,
			network,
			rootPassword,
			adminPassword,
			storage
		};
	}

	function applyInstallState(state: {
		steps?: { id: string; label: string; detail?: string; state: string; error?: string }[];
		logs?: string[];
		running?: boolean;
		done?: boolean;
		error?: string;
	}) {
		if (Array.isArray(state.steps)) {
			installSteps = state.steps.map((item) => ({
				id: item.id,
				label: item.label,
				detail: item.error ? `${item.detail ?? ''} — ${item.error}`.trim() : (item.detail ?? ''),
				state: item.state
			}));
		}
		if (Array.isArray(state.logs)) installLogs = state.logs.slice(-MAX_LOG_LINES);
		const total = installSteps.length || 1;
		const settled = installSteps.filter(
			(item) => item.state === 'done' || item.state === 'skipped' || item.state === 'failed'
		).length;
		installPercent = Math.round((settled / total) * 100);
		installRunning = state.running === true;
		installError = state.error ?? '';
		if (state.done) {
			installPercent = 100;
			step = 'done';
		}
	}

	async function refreshInstallState() {
		try {
			const res = await fetch('/api/install');
			if (!res.ok) return;
			applyInstallState(await res.json());
		} catch {
		}
	}

	function disconnectInstallEvents() {
		if (installSocket) {
			installSocket.onmessage = null;
			installSocket.close();
			installSocket = undefined;
		}
	}

	function connectInstallEvents() {
		disconnectInstallEvents();
		const proto = location.protocol === 'https:' ? 'wss' : 'ws';
		try {
			installSocket = new WebSocket(`${proto}://${location.host}/api/events`);
		} catch {
			installSocket = undefined;
			return;
		}
		installSocket.onmessage = (event) => {
			try {
				const msg = JSON.parse(String(event.data));
				if (msg.type === 'state' && msg.state) applyInstallState(msg.state);
				else if (msg.type === 'log' && typeof msg.line === 'string') {
					installLogs = [...installLogs.slice(-(MAX_LOG_LINES - 1)), msg.line];
				}
			} catch {
			}
		};
	}

	async function beginInstall() {
		installSteps = [];
		installLogs = [];
		installPercent = 0;
		installError = '';
		installRunning = true;
		step = 'progress';

		try {
			const res = await fetch('/api/install', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(buildPlan())
			});
			const json = await res.json().catch(() => null);
			if (!res.ok) {
				installError = json?.error ?? `The install could not start (HTTP ${res.status}).`;
				installRunning = false;
				return;
			}
			applyInstallState(json ?? {});
			connectInstallEvents();
			if (installPoller) clearInterval(installPoller);
			installPoller = setInterval(() => {
				if (!installRunning) {
					if (installPoller) clearInterval(installPoller);
					installPoller = undefined;
					disconnectInstallEvents();
					return;
				}
				refreshInstallState();
			}, 2500);
		} catch (e) {
			installError = String(e);
			installRunning = false;
		}
	}

	async function power(action: 'reboot' | 'poweroff') {
		try {
			await fetch('/api/power', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ action })
			});
		} catch {
		}
	}

	onDestroy(() => {
		disconnectInstallEvents();
		if (installPoller) clearInterval(installPoller);
	});

	onMount(() => {
		dark = document.documentElement.classList.contains('dark');
		initializeAccess();
	});

	async function loadInstaller() {
		try {
			system = await (await fetch('/api/system')).json();
			uplink = system?.interfaces?.[0]?.name ?? '';
		} catch {
			system = null;
		}
		try {
			const list = await (await fetch('/api/timezones')).json();
			if (Array.isArray(list) && list.length) tzList = list;
		} catch {
		}
		try {
			const list = await (await fetch('/api/keymaps')).json();
			if (Array.isArray(list) && list.length) kmList = list;
		} catch {
		}
		try {
			rawDisks = await (await fetch('/api/disks')).json();
			const candidates = rawDisks.filter((disk) => !disk.boot);
			if (candidates.length === 1) {
				const device = `/dev/${candidates[0].name}`;
				pool = {
					...pool,
					vdevs: pool.vdevs.map((vdev, index) =>
						index === 0 ? { ...vdev, disks: [device] } : vdev
					)
				};
			}
		} catch {
			rawDisks = [];
		}
		try {
			const res = await fetch('/api/install');
			if (res.ok) {
				const state = await res.json();
				if (state?.running || state?.done) {
					applyInstallState(state);
					step = state.done ? 'done' : 'progress';
					connectInstallEvents();
				}
			}
		} catch {
		}
	}
</script>

<svelte:window oncontextmenu={(event) => event.preventDefault()} />

{#snippet encryptionOptions(ufs: boolean)}
	{@const suffix = ufs ? '-ufs' : ''}
	<div class="space-y-3 rounded-lg border border-border bg-muted/20 p-4">
		<div class="flex items-start gap-3">
			<Checkbox id={`encrypt${suffix}`} bind:checked={encrypt} class="mt-0.5" />
			<div class="min-w-0 flex-1 space-y-0.5">
				<div class="text-sm leading-snug">
					<Label for={`encrypt${suffix}`} class="inline leading-snug font-medium">
						Encrypt the {ufs ? 'root' : 'system'} (GELI)
					</Label>
					{#if encrypt}
						<span class="text-muted-foreground text-xs">
							<span class="mx-2">-</span>The passphrase is requested at every boot,
							before FreeBSD starts.
						</span>
					{:else}
						<span class="text-amber-600 dark:text-amber-400 text-xs">
							<span class="mx-2">-</span><TriangleAlert
								class="inline size-3.5 shrink-0 align-[-0.125em]"
							/> Without encryption the system boots without a passphrase.
						</span>
					{/if}
				</div>
			</div>
		</div>
		{#if encrypt}
			<div class="grid gap-4 sm:grid-cols-2">
				<div class="space-y-2">
					<Label for={`passphrase${suffix}`}>Passphrase</Label>
					<Input
						id={`passphrase${suffix}`}
						type="text"
						autocomplete="off"
						spellcheck="false"
						class="[-webkit-text-security:disc]"
						placeholder="••••••••"
						bind:value={passphrase}
					/>
				</div>
				<div class="space-y-2">
					<Label for={`passphrase2${suffix}`}>Confirm passphrase</Label>
					<Input
						id={`passphrase2${suffix}`}
						type="text"
						autocomplete="off"
						spellcheck="false"
						class="[-webkit-text-security:disc]"
						placeholder="••••••••"
						bind:value={passphrase2}
					/>
				</div>
			</div>
			{#if passphrase.length > 0 && passphrase.length < 8}
				<p class="text-xs text-destructive">Use at least 8 characters.</p>
			{:else if passphrase2.length > 0 && passphrase !== passphrase2}
				<p class="text-xs text-destructive">Passphrases do not match.</p>
			{/if}
		{/if}
	</div>
{/snippet}

<div class="flex min-h-screen flex-col bg-background text-foreground">
	<header class="border-b border-border bg-background">
		<div class="mx-auto grid max-w-6xl grid-cols-[1fr_auto_1fr] items-center gap-3 px-6 py-2.5">
			<div></div>
			<div class="flex items-center justify-center gap-4">
				<img
					src={dark ? '/logo/freebsd.svg' : '/logo/freebsd-black.svg'}
					alt="FreeBSD"
					class="h-6 w-auto"
				/>
				<span class="text-lg leading-none text-muted-foreground">+</span>
				<div class="flex items-center gap-2.5">
					<img src={dark ? '/logo/white.svg' : '/logo/black.svg'} alt="Sylve" class="h-6 w-auto" />
					<p class="text-sm font-normal tracking-[0.35em]">SYLVE</p>
				</div>
			</div>
			<div class="flex items-center justify-end gap-2">
				{#if system?.kernel}<Badge variant="outline">{system.kernel}</Badge>{/if}
				{#if system?.bootMethod}<Badge variant="outline">{system.bootMethod}</Badge>{/if}
				{#if access?.authenticated}<AboutDialog dark={dark} />{/if}
				<Button
					variant="outline"
					size="icon"
					class="h-[22px] w-[22px] rounded-md [&_svg]:size-3.5"
					onclick={toggleTheme}
					aria-label="Toggle theme"
					title="Toggle theme"
				>
					{#if dark}<Sun class="size-3.5" />{:else}<Moon class="size-3.5" />{/if}
				</Button>
			</div>
		</div>
	</header>

	<div class={cn('mx-auto w-full max-w-4xl flex-1 px-6 py-3', !access?.authenticated && 'flex items-center justify-center')}>
		<main class={cn('min-w-0', !access?.authenticated && 'w-full')}>
			{#if access === null}
				<div class="flex items-center justify-center gap-2 py-8 text-sm text-muted-foreground" role="status">
					<LoaderCircle class="size-4 animate-spin motion-reduce:animate-none" />
					<span>Loading installer…</span>
				</div>
			{:else if !access.authenticated}
				<Card.Root class="mx-auto max-w-md gap-6 rounded-[10px] py-[26px] dark:border-[#373737] dark:text-[#eee]">
					<Card.Header class="items-center gap-0 px-[26px] text-center">
						<div class="mx-auto mb-3 grid size-10 place-items-center rounded-xl bg-muted/50">
							<LockKeyhole class="size-4 text-foreground/70" />
						</div>
						<Card.Title class="text-xl font-medium leading-7 tracking-tight">Connect to installer</Card.Title>
						<Card.Description class="mt-2 leading-[21px] dark:text-[#b1b1b1]">
							Enter the password shown beside the QR code on the target machine, or on its text console.
						</Card.Description>
					</Card.Header>
					<Card.Content class="px-[26px]">
						<form aria-busy={accessBusy} onsubmit={(event) => { event.preventDefault(); authenticate(); }}>
							<div class="space-y-1.5">
								<Label for="installer-access-password" class="justify-start text-[13px] font-medium leading-5">Installer access password</Label>
								<div class="relative">
									<KeyRound class="pointer-events-none absolute left-3.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground/60" />
									<Input
										id="installer-access-password"
										type={accessPasswordVisible ? 'text' : 'password'}
										autocomplete="one-time-code"
										spellcheck={false}
										class="h-12 rounded-[8px] bg-background/40 px-11 text-left text-2xl font-normal shadow-none placeholder:text-base placeholder:tracking-[0.3em] placeholder:text-muted-foreground/35 focus-visible:ring-2 aria-invalid:focus-visible:ring-0 md:text-2xl dark:bg-[#1e1e1e] dark:aria-invalid:border-[#bf5863] dark:aria-invalid:focus-visible:border-[#bf5863]"
										placeholder="••••••••••••"
										disabled={accessBusy}
										aria-invalid={!!accessError}
										aria-describedby="installer-access-status"
										bind:value={accessPassword}
									/>
									<button
										type="button"
										class="absolute right-1.5 top-1/2 grid size-8 -translate-y-1/2 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring disabled:pointer-events-none"
										aria-label={accessPasswordVisible ? 'Hide password' : 'Show password'}
										aria-pressed={accessPasswordVisible}
										disabled={accessBusy}
										onclick={() => (accessPasswordVisible = !accessPasswordVisible)}
									>
										{#if accessPasswordVisible}<EyeOff class="size-4" />{:else}<Eye class="size-4" />{/if}
									</button>
								</div>
							</div>
							<div id="installer-access-status" class="text-[13px] leading-4 text-destructive dark:text-[#f28d98]" aria-live="polite">
								{#if accessError}
									<p class="flex items-center gap-1.5 py-2" role="alert" transition:slide={{ duration: prefersReducedMotion.current ? 0 : 160, easing: cubicOut }}>
										<CircleAlert class="size-3.5 shrink-0" />
										<span>{accessError}</span>
									</p>
								{/if}
							</div>
							<Button type="submit" class="mt-3 h-11 w-full gap-2 rounded-[8px] dark:bg-[#ebebeb] dark:text-[#242424] dark:hover:bg-[#dedede]" disabled={accessBusy || !accessPassword.trim()}>
								{#if accessBusy}
									<LoaderCircle class="size-4 animate-spin" />
									Connecting…
								{:else}
									Connect <ArrowRight class="size-4" />
								{/if}
							</Button>
						</form>
					</Card.Content>
				</Card.Root>
			{:else if step === 'progress'}
				<Card.Root class="gap-5 py-5">
					<Card.Header class="space-y-3">
						<div class="flex items-center justify-between gap-3">
							<Card.Title>
								{installRunning ? 'Installing FreeBSD + Sylve' : installError ? 'Install failed' : 'Install finished'}
							</Card.Title>
							<Badge variant="outline">{installRunning ? 'Running' : installError ? 'Failed' : 'Done'}</Badge>
						</div>
						<Progress value={installPercent} progressClass="bg-primary" />
						<p class="text-xs text-muted-foreground">
							{installPercent}% ·
							{installSteps.filter((s) => s.state === 'done' || s.state === 'skipped').length} of {installSteps.length}
							steps
						</p>
					</Card.Header>
					<Card.Content class="space-y-4">
						<ul class="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
							{#each installSteps as item (item.id)}
								<li class="flex items-center gap-3 rounded-lg border border-border px-3 py-2">
									<span
										class={cn(
											'size-2.5 shrink-0 rounded-full',
											item.state === 'done'
												? 'bg-emerald-500'
												: item.state === 'running'
													? 'animate-pulse bg-primary'
													: item.state === 'skipped'
														? 'bg-muted-foreground/50'
														: 'bg-muted-foreground/30'
										)}
									></span>
									<div class="min-w-0">
										<p class="text-sm">{item.label}</p>
										<p class="truncate text-xs text-muted-foreground">{item.detail}</p>
									</div>
									<Badge variant="outline" class="ml-auto shrink-0 capitalize">{item.state || 'pending'}</Badge>
								</li>
							{/each}
						</ul>
						<div
							class="max-h-36 overflow-y-auto rounded-lg border border-border bg-muted/40 p-3 font-mono text-[11px] leading-relaxed text-muted-foreground"
						>
							{#each installLogs as line}<div>{line}</div>{/each}
						</div>
						{#if installError}
							<div class="border-destructive/40 bg-destructive/5 space-y-2 rounded-lg border p-3">
								<p class="flex items-start gap-2 text-xs text-destructive">
									<TriangleAlert class="mt-0.5 size-3.5 shrink-0" />
									<span class="min-w-0">{installError}</span>
								</p>
								<Button variant="outline" size="sm" onclick={() => (step = 'summary')}>
									Back to summary
								</Button>
							</div>
						{:else if !installRunning}
							<div class="flex items-center gap-2 text-xs text-emerald-600 dark:text-emerald-400">
								<Check class="size-3.5 shrink-0" />
								<p>Install complete. Reboot when you are ready.</p>
							</div>
						{/if}
					</Card.Content>
				</Card.Root>
			{:else if step === 'done'}
				<Card.Root class="gap-5 py-5">
					<Card.Header class="gap-3">
						<Card.Title class="flex items-center gap-2 leading-snug">
							<Check class="size-5 shrink-0 text-emerald-600 dark:text-emerald-400" />
							<span>FreeBSD + Sylve is installed</span>
						</Card.Title>
						<Card.Description>
							Reboot into the installed system to start Sylve.
						</Card.Description>
					</Card.Header>
					<Card.Content class="space-y-5">
						{#if sylveURL}
							<div class="flex items-center gap-4 rounded-lg border border-border bg-muted/30 p-4">
								<div class="min-w-0 flex-1 space-y-1">
									<p class="text-xs text-muted-foreground">Sylve will be available at</p>
									<p class="truncate text-sm">{sylveURL}</p>
									<p class="text-xs text-muted-foreground">
										SSH uses port 22. The local FreeBSD console remains available.
									</p>
								</div>
								<button
									type="button"
									class="shrink-0 rounded-md transition-opacity hover:opacity-90 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
									onclick={() => enlargeQR(sylveURL)}
									aria-label="Enlarge Sylve QR code"
									title="Enlarge QR code"
								>
									<img
										src={`/api/qr?text=${encodeURIComponent(sylveURL)}`}
										alt="Sylve URL QR code"
										class="size-28 rounded-md border border-border bg-white p-3"
									/>
								</button>
							</div>
						{:else}
							<div class="flex items-start gap-3 rounded-lg border border-border bg-muted/30 p-4">
								<Globe class="mt-0.5 size-5 shrink-0 text-muted-foreground" />
								<div class="space-y-2">
									<p class="text-sm font-medium">
										{system?.interfaces?.length === 0 ? 'No network interface detected' : 'Network address not available yet'}
									</p>
									<p class="text-xs text-muted-foreground">
										{#if system?.interfaces?.length === 0}
											To access the web UI, connect a network interface and configure the Sylve switch
											from the local FreeBSD console after rebooting.
										{:else}
											After rebooting, use the local FreeBSD console to find or configure the network
											address.
										{/if}
										{' '}Sylve uses HTTPS on port 8181.
									</p>
								</div>
							</div>
						{/if}
						<div class="flex flex-wrap gap-2">
							<Button onclick={() => power('reboot')}>
								<RotateCw class="size-3.5" /> Reboot
							</Button>
							<Button variant="outline" onclick={() => power('poweroff')}>
								<Power class="size-3.5" /> Power off
							</Button>
						</div>
						<p class="text-xs text-muted-foreground">
							Remove the installation media before rebooting. Sylve configures its switch on the
							first boot.
							{#if sylveURL}
								{' '}The machine may be offline briefly while the daemon starts.
							{/if}
						</p>
					</Card.Content>
				</Card.Root>
			{:else}
				<Card.Root class="gap-4 py-5">
					<Card.Header>
						<nav aria-label="steps" class="flex flex-wrap items-center justify-center gap-1">
							{#each FLOW as item, index (item.id)}
								<button
									class="flex items-center gap-1.5 transition-opacity disabled:cursor-not-allowed disabled:opacity-50"
									disabled={!licenseAccepted && item.id !== 'checks'}
									onclick={() => (step = item.id)}
								>
									<span
										class={cn(
											'grid h-5 w-5 shrink-0 place-items-center rounded-full border text-xs transition-colors',
											step === item.id
												? 'border-primary bg-primary text-primary-foreground'
												: index < stepIndex
													? 'border-primary/60 text-primary'
													: 'border-border text-muted-foreground'
										)}
									>
										{index < stepIndex ? '✓' : index + 1}
									</span>
									<span
										class={cn(
											'text-xs',
											step === item.id ? 'text-foreground' : 'text-muted-foreground'
										)}
									>
										{item.label}
									</span>
								</button>
								{#if index < FLOW.length - 1}
									<span class="h-px w-2 bg-border"></span>
								{/if}
							{/each}
						</nav>
					</Card.Header>

					<Card.Content class="space-y-6">
						{#if step === 'checks'}
							<div class="space-y-2">
								{#if licenseAccepted}
									<div
										class="animate-in fade-in slide-in-from-bottom-2 flex items-center justify-between gap-4 duration-500"
									>
										<p class="flex min-w-0 items-center gap-1.5 text-base font-medium">
											<ListChecks class="text-muted-foreground size-4 shrink-0" />
											Let's check if everything looks right on this machine
										</p>
										<Button
											variant="outline"
											size="icon"
											class="size-7 shrink-0"
											onclick={runChecks}
											disabled={preflightRunning}
											aria-label="Re-run checks"
											title="Re-run checks"
										>
											<RefreshCw class={cn('size-3.5', preflightRunning && 'animate-spin')} />
										</Button>
									</div>
								{/if}

								{#if !licenseAccepted}
									{#if licenseDeclined}
										<div
											class="animate-in fade-in zoom-in-95 flex flex-col items-center gap-3 rounded-lg border border-border bg-muted/30 p-8 text-center duration-500"
										>
											<div class="flex flex-col items-center gap-1">
												<SadCat class="size-24" />
												<div class="space-y-1">
													<p class="text-base font-medium">We're sad to see you go</p>
													<p class="text-muted-foreground text-sm">
														The license has to be accepted before anything is installed.
													</p>
												</div>
											</div>
											<div class="flex items-center gap-2">
												<Button variant="outline" onclick={() => (licenseDeclined = false)}>
													Back to License
												</Button>
												<Button variant="outline" onclick={() => power('poweroff')}>
													Shut Down
												</Button>
											</div>
										</div>
									{:else}
										<div class="animate-in fade-in space-y-2 duration-500">
											<p class="flex items-center gap-1.5 text-base font-medium">
												<ScrollText class="text-muted-foreground size-4" />
												License
											</p>
											<pre
												class="text-muted-foreground max-h-96 overflow-y-auto rounded-lg border border-border bg-muted/30 p-3 text-[11px] leading-relaxed whitespace-pre-wrap">{LICENSE_TEXT}</pre>
											<div class="flex items-center justify-between gap-3">
												<Button variant="outline" onclick={() => (licenseDeclined = true)}>
													I Disagree
												</Button>
												<Button onclick={agreeLicense}>I Agree</Button>
											</div>
										</div>
									{/if}
								{:else}
								{#if preflightError}
									<div
										class="border-destructive/40 bg-destructive/5 text-destructive rounded-lg border p-3 text-xs"
									>
										Could not run the checks ({preflightError}).
									</div>
								{/if}

								{#if preflightRunning && preflight.length === 0}
									<ul class="space-y-1">
										{#each Array.from({ length: checkSkeletonCount }) as _, i (i)}
											<li class="animate-pulse rounded-lg border border-border px-3 py-1">
												<div class="bg-muted-foreground/20 h-5 w-2/5 rounded"></div>
												<div class="bg-muted-foreground/20 mt-1 h-3 w-3/5 rounded"></div>
											</li>
										{/each}
									</ul>
								{:else}
									{#key preflight}
										<ul class="animate-in fade-in space-y-1 duration-300">
											{#each preflight as item (item.id)}
										<li
											class={cn(
												'flex items-center gap-3 rounded-lg border px-3 py-1',
												item.status === 'fail'
													? 'border-destructive/40 bg-destructive/5'
													: item.status === 'warn'
														? 'border-amber-500/40 bg-amber-500/5'
														: 'border-border'
											)}
										>
											<div class="min-w-0 flex-1">
												<p class="text-sm font-medium">{item.label}</p>
												<p class="text-xs text-muted-foreground">{item.detail}</p>
											</div>
											{#if item.id === 'network'}
												<button
													type="button"
													class="focus-visible:border-ring focus-visible:ring-ring/50 inline-flex w-fit shrink-0 cursor-pointer items-center justify-center rounded-md border px-2 py-0.5 text-xs font-medium whitespace-nowrap transition-[color,box-shadow] hover:bg-accent hover:text-accent-foreground focus-visible:ring-[3px]"
													onclick={() => (liveNetworkOpen = true)}
													aria-label="Configure live network"
													title="Configure live network"
												>
													<Wrench class="size-3" />
												</button>
											{/if}
											<Badge
												variant="outline"
												title={item.error || item.detail}
												class={cn(
													'shrink-0 gap-1',
													item.status === 'ok'
														? 'text-emerald-600 dark:text-emerald-400'
														: item.status === 'warn'
															? 'text-amber-600 dark:text-amber-400'
															: 'text-destructive'
												)}
											>
												{#if item.status === 'ok'}
													<Check class="size-3" />
												{:else if item.status === 'warn'}
													<TriangleAlert class="size-3" />
												{:else}
													<X class="size-3" />
												{/if}
												{item.status === 'ok' ? 'OK' : item.status === 'warn' ? 'WARN' : 'FAIL'}
											</Badge>
										</li>
											{/each}
										</ul>
									{/key}
								{/if}

								{/if}
							</div>
						{:else if step === 'hostname'}
							<div class="space-y-4">
								<div class="space-y-2">
									<Label for="hostname">Hostname (FQDN)</Label>
									<Input
										id="hostname"
										bind:value={hostname}
										placeholder="sylve.example.com"
										class="h-9 w-full"
									/>
									{#if !hostnameOK}
										<p class="text-xs text-destructive">
											Use letters, digits, dots and dashes; each label must start and end with a
											letter or digit.
										</p>
									{:else}
										<p class="text-xs text-muted-foreground">
											Short names are fine; the domain suffix is optional.
										</p>
									{/if}
								</div>
								<TimezoneMap
									value={timezone}
									label={prettyTZ(timezone)}
									onSelect={(tz) => (timezone = tz)}
								/>
								<div class="grid gap-4 sm:grid-cols-2">
									<div class="space-y-2">
										<Label for="timezone">Timezone</Label>
										<Combobox
											bind:value={timezone}
											options={tzOptions}
											placeholder="Select a timezone"
											searchPlaceholder="Search timezones…"
										/>
									</div>
									<div class="space-y-2">
										<Label for="keymap">Keyboard layout</Label>
										<Combobox
											bind:value={keymap}
											options={kmOptions}
											placeholder="Select a keymap"
											searchPlaceholder="Search keymaps…"
										/>
									</div>
								</div>
							</div>
						{:else if step === 'disk'}
							<div class="space-y-6">
								<PartitionModeChoice bind:value={partitionMode} onSelect={selectMode} />

								{#if partitionMode === 'zfs'}
									<div class="space-y-5">
									<div class="space-y-3">
										<div class="flex items-start justify-between gap-4">
											<div class="space-y-1">
												<p class="text-sm font-medium">Target pool</p>
												<p class="text-xs text-muted-foreground">
													{configDisks(pool).length
														? `${pool.name} · ${poolLayoutLabel(pool)} · ${formatBytes(usable)} Usable`
														: 'No disks selected yet.'}
												</p>
											</div>
											<div class="flex items-center gap-2">
												<Button
													variant="outline"
													size="sm"
													onclick={() => {
														reloadDisks();
														manageOpen = true;
													}}
												>
													<Wrench class="size-3.5" /> Manage disks
												</Button>
												<Button
													variant="outline"
													size="sm"
													onclick={() => {
														reloadDisks();
														poolDialogOpen = true;
													}}
												>
													<HardDrive class="size-3.5" /> Configure pool
												</Button>
											</div>
										</div>

										<div class="rounded-lg border border-border bg-muted/30 p-3">
											{#if poolMembers.length === 0}
												<div class="flex min-h-16 flex-col items-center justify-center gap-1 text-center">
													<p class="text-sm">Choose the disks for the root pool.</p>
													<p class="text-xs text-muted-foreground">
														The installer media is never offered as a target.
													</p>
												</div>
											{:else}
												<div
													class="grid max-h-48 grid-cols-[repeat(auto-fill,minmax(76px,1fr))] items-start gap-4 overflow-y-auto pr-1"
												>
													{#each poolMembers as member (member)}
														{#if isPartitionMember(member)}
															<div class="text-center">
																<Icon
																	icon={partitionOutlined}
																	class="mx-auto size-9 rotate-90 text-blue-500"
																/>
																<div class="max-w-20 truncate text-xs">{member}</div>
															</div>
														{:else}
															{@const disk = poolDisks.find(
																(d) => shortDevice(d.device) === member
															)}
															<div class="text-center">
																<DiskKindIcon kind={disk?.kind ?? 'other'} class="mx-auto size-9" />
																<div class="max-w-20 truncate text-xs">{member}</div>
																<div class="text-xs text-muted-foreground">
																	{formatBytes(disk?.sizeBytes ?? 0)}
																</div>
																{#if disk?.model}
																	<div
																		class="text-muted-foreground max-w-24 truncate text-[10px]"
																		title={disk.model}
																	>
																		{disk.model}
																	</div>
																{/if}
															</div>
														{/if}
													{/each}
												</div>
												<p class="text-muted-foreground mt-3 border-t pt-3 text-xs">
													Layout: {poolLayoutLabel(pool)} · Usable: {formatBytes(usable)}
												</p>
												{#if targetTooSmall}
													<p class="text-destructive mt-1 text-xs">
														At least 8 GiB of data space must remain after layout overhead
													</p>
												{/if}
											{/if}
										</div>
									</div>

									<div class="space-y-4 border-t border-border pt-4">
										<div class="space-y-2">
											<Label for="zfs-swap">Swap</Label>
											<Input
												id="zfs-swap"
												bind:value={zfsSwap}
												class={cn('h-9', zfsSwapInvalid && 'border-destructive')}
												onblur={() => (zfsSwap = normalizeSizeInput(zfsSwap) ?? zfsSwap)}
											/>
											<p class="text-xs text-muted-foreground">
												Swap partition on every selected whole disk, enter 0 for none.
												{#if encrypt}
													{' '}Any configured swap is encrypted automatically with a fresh key at every boot.
												{/if}
											</p>
											{#if zfsSwapInvalid}
												<p class="text-xs text-destructive">Use a size like 2G, 2048M or 512 MiB.</p>
											{/if}
										</div>

										{@render encryptionOptions(false)}
									</div>
									</div>
								{:else}
									<div class="space-y-5">
										<div class="space-y-2">
											<div class="flex items-center justify-between gap-3">
												<Label>Disk</Label>
												<Button
													variant="outline"
													size="sm"
													onclick={() => {
														reloadDisks();
														manageOpen = true;
													}}
												>
													<Wrench class="size-3.5" /> Manage disks
												</Button>
											</div>
											<div class="rounded-lg border border-border bg-muted/30 p-2">
												<div class="max-h-36 space-y-1.5 overflow-y-auto">
													{#each poolDisks as disk (disk.device)}
														<button
															class={cn(
																'flex w-full items-center gap-3 rounded-md border px-3 py-2 text-left transition-colors',
																ufsDisk === disk.device
																	? 'border-primary bg-primary/5'
																	: 'border-transparent hover:bg-muted/60'
															)}
															onclick={() => selectUfsDisk(disk.device)}
														>
															<DiskKindIcon kind={disk.kind} class="size-5 shrink-0" />
															<span class="min-w-0 flex-1">
																<span class="block truncate text-sm">
																	{shortDevice(disk.device)} · {diskLabel(disk.kind)}
																</span>
																<span class="text-muted-foreground block truncate text-xs">
																	{disk.model || 'unknown model'} · {formatBytes(disk.sizeBytes)}
																</span>
															</span>
															{#if ufsDisk === disk.device}
																<span class="text-primary shrink-0 text-xs">Selected</span>
															{/if}
														</button>
													{:else}
														<p class="text-muted-foreground p-2 text-sm">
															No installable disks detected.
														</p>
													{/each}
												</div>
											</div>
										</div>

										<div class="space-y-2">
											<Label>Layout</Label>
											<div class="grid gap-2 sm:grid-cols-2">
												<button
													class={cn(
														'rounded-lg border p-3 text-left transition-colors',
														ufsTarget === 'entire'
															? 'border-primary bg-primary/5'
															: 'border-border hover:bg-muted/40'
													)}
													onclick={() => selectUfsTarget('entire')}
												>
													<span class="text-sm font-medium">Entire disk</span>
													<span class="text-muted-foreground mt-0.5 block text-xs">
														Repartition the whole disk — everything on it is erased.
													</span>
												</button>
												<button
													class={cn(
														'rounded-lg border p-3 text-left transition-colors',
														ufsTarget === 'free'
															? 'border-primary bg-primary/5'
															: 'border-border hover:bg-muted/40',
														!ufsHasScheme && 'cursor-not-allowed opacity-50'
													)}
													onclick={() => selectUfsTarget('free')}
													disabled={!ufsHasScheme}
													title={ufsHasScheme
														? 'Keep the existing partitions and install into the free space'
														: 'A GPT partition table is required'}
												>
													<span class="text-sm font-medium">Use free space</span>
													<span class="text-muted-foreground mt-0.5 block text-xs">
														Keep existing partitions; only the free space is used.
													</span>
												</button>
											</div>

											<div
												class="flex flex-wrap items-center gap-1.5 rounded-lg border border-border bg-muted/30 p-3 text-xs"
											>
												{#if ufsTarget === 'free' && ufsRootEstimate === 0}
													<span
														class="border-destructive/40 bg-background text-destructive flex items-center gap-1.5 rounded-md border px-2 py-1"
													>
														<TriangleAlert class="size-3.5 shrink-0" />
														No free space for the root filesystem
													</span>
												{:else}
													<span class="bg-background rounded-md border border-border px-2 py-1">
														efi · 260M (UEFI)
													</span>
													<span class="bg-background rounded-md border border-border px-2 py-1">
														freebsd-boot · 512k (BIOS)
													</span>
													<span class="bg-background rounded-md border border-border px-2 py-1">
														root · UFS · {formatBytes(ufsRootEstimate)}
													</span>
												{/if}
												<span class="bg-background rounded-md border border-border px-2 py-1">
													swap · {ufsSwapLabel}
												</span>
											</div>
											{#if targetTooSmall}
												<p class="text-destructive text-xs">
													At least 8 GiB of root space must remain after layout overhead
												</p>
											{/if}
										</div>

										<div class="space-y-4 border-t border-border pt-4">
											<div class="space-y-2">
												<Label for="ufs-swap">Swap</Label>
												<Input
													id="ufs-swap"
													bind:value={ufsSwap}
													class={cn('h-9', ufsSwapInvalid && 'border-destructive')}
													onblur={() => (ufsSwap = normalizeSizeInput(ufsSwap) ?? ufsSwap)}
												/>
												<p class="text-xs text-muted-foreground">
													{ufsDiskInfo
														? `Suggested: ${formatSize(ufsSwapSuggestion)} — a twentieth of the free space, capped at 4 GB. Enter 0 to skip swap.`
														: 'Enter 0 to skip swap.'}
													{#if encrypt}
														{' '}Any configured swap is encrypted automatically with a fresh key at every boot.
													{/if}
												</p>
												{#if ufsSwapInvalid}
													<p class="text-xs text-destructive">
														Use a size like 2G, 2048M or 512 MiB.
													</p>
												{/if}
											</div>

											{@render encryptionOptions(true)}
										</div>

									</div>
								{/if}
							{#if poolDialogOpen}
								<PoolDialog
									disks={poolDisks}
									config={pool}
									onApply={(config) => {
										pool = config;
										poolDialogOpen = false;
									}}
									onClose={() => (poolDialogOpen = false)}
								/>
							{/if}

							{#if manageOpen}
								<DiskManager
									disks={allDisks}
									onClose={() => {
										manageOpen = false;
										reloadDisks();
									}}
									onDisks={(disks) => (rawDisks = disks)}
								/>
							{/if}
							</div>
						{:else if step === 'password'}
							<div class="space-y-4">
								<div class="grid gap-4 sm:grid-cols-2">
									<div class="space-y-2">
										<Label for="root-password">Root password</Label>
										<Input
											id="root-password"
											type="text"
											autocomplete="off"
											spellcheck="false"
											class="[-webkit-text-security:disc]"
											placeholder="••••••••"
											bind:value={rootPassword}
										/>
									</div>
									<div class="space-y-2">
										<Label for="root-password2">Confirm root password</Label>
										<Input
											id="root-password2"
											type="text"
											autocomplete="off"
											spellcheck="false"
											class="[-webkit-text-security:disc]"
											placeholder="••••••••"
											bind:value={rootPassword2}
										/>
									</div>
									<div class="space-y-2 sm:col-span-2">
										<Label for="admin-password">Sylve admin password</Label>
										<Input
											id="admin-password"
											type="text"
											minlength={8}
											aria-invalid={adminPassword.length > 0 && !adminPasswordValid}
											aria-describedby="admin-password-help"
											autocomplete="off"
											spellcheck="false"
											class="[-webkit-text-security:disc]"
											placeholder="••••••••"
											bind:value={adminPassword}
										/>
										<p
											id="admin-password-help"
											class={cn(
												'text-xs text-muted-foreground',
												adminPassword.length > 0 && !adminPasswordValid && 'text-destructive'
											)}
										>
											At least 8 characters. Used for the Sylve web UI (user <code>admin</code>)
											— changeable after the first login.
										</p>
									</div>
								</div>
								{#if rootPassword.length > 0 && rootPassword !== rootPassword2}
									<p class="text-xs text-destructive">Root passwords do not match.</p>
								{/if}
							</div>
						{:else if step === 'network'}
							<div class="space-y-5">
								{#if (system?.interfaces ?? []).length === 0}
									<div class="bg-muted/30 flex items-start gap-3 rounded-lg border p-4">
										<TriangleAlert class="text-muted-foreground mt-0.5 size-4 shrink-0" />
										<div class="space-y-1 text-sm">
											<p class="font-medium">No network interfaces detected</p>
											<p class="text-muted-foreground text-xs">
												Sylve creates the switch without a port. Attach the real interface
												later from the console:
												<code>sylve switches edit --type standard --id &lt;id&gt; --ports &lt;nic&gt;</code>
											</p>
										</div>
									</div>
								{:else}
									<div class="space-y-2">
										<Label>Uplink</Label>
										<div class="max-h-36 space-y-2 overflow-y-auto">
											{#each system?.interfaces ?? [] as iface (iface.name)}
												<button
													class={cn(
														'flex w-full items-center justify-between rounded-lg border px-3 py-2.5 text-left transition-colors',
														uplink === iface.name
															? 'border-primary bg-primary/5'
															: 'border-border hover:bg-muted/50'
													)}
													onclick={() => (uplink = iface.name)}
												>
													<span class="flex items-center gap-2 text-sm">
														<Globe class="size-4 text-muted-foreground" />
														{iface.name}
													</span>
													<span class="text-xs text-muted-foreground">
														{(iface.ipv4 ?? []).join(', ') || 'no address'}
													</span>
												</button>
											{/each}
										</div>
									</div>

								<div class="space-y-2">
									<Label>Addressing</Label>
									<div class="flex gap-2">
										<Button
											variant={networkMode === 'dhcp' ? 'default' : 'outline'}
											size="sm"
											onclick={() => (networkMode = 'dhcp')}
										>
											DHCP
										</Button>
										<Button
											variant={networkMode === 'static' ? 'default' : 'outline'}
											size="sm"
											onclick={() => (networkMode = 'static')}
										>
											Static
										</Button>
									</div>
								</div>

								<div class="space-y-2">
									<Label>IPv6</Label>
									<div class="flex gap-2">
										<Button
											variant={networkMode6 === 'off' ? 'default' : 'outline'}
											size="sm"
											onclick={() => (networkMode6 = 'off')}
										>
											Off
										</Button>
										<Button
											variant={networkMode6 === 'slaac' ? 'default' : 'outline'}
											size="sm"
											onclick={() => (networkMode6 = 'slaac')}
										>
											SLAAC
										</Button>
										<Button
											variant={networkMode6 === 'static' ? 'default' : 'outline'}
											size="sm"
											onclick={() => (networkMode6 = 'static')}
										>
											Static
										</Button>
									</div>
								</div>

								{#if networkMode === 'static'}
									<div class="grid gap-4 sm:grid-cols-3">
										<div class="space-y-2">
											<Label for="static-address">IPv4 address (CIDR)</Label>
											<Input
												id="static-address"
												placeholder="192.168.1.50/24"
												bind:value={staticAddress}
											/>
										</div>
										<div class="space-y-2">
											<Label for="static-gateway">IPv4 gateway</Label>
											<Input
												id="static-gateway"
												placeholder="192.168.1.1"
												bind:value={staticGateway}
											/>
										</div>
										<div class="space-y-2">
											<Label for="static-dns">DNS</Label>
											<Input id="static-dns" bind:value={staticDNS} />
										</div>
									</div>
								{/if}

								{#if networkMode6 === 'static'}
									<div class="grid gap-4 sm:grid-cols-3">
										<div class="space-y-2">
											<Label for="static-address6">IPv6 address (CIDR)</Label>
											<Input
												id="static-address6"
												placeholder="fd00::2/64"
												bind:value={staticAddress6}
											/>
										</div>
										<div class="space-y-2">
											<Label for="static-gateway6">IPv6 gateway</Label>
											<Input
												id="static-gateway6"
												placeholder="fd00::1"
												bind:value={staticGateway6}
											/>
										</div>
										<div class="space-y-2">
											<Label for="static-dns6">IPv6 DNS</Label>
											<Input id="static-dns6" bind:value={staticDNS6} />
										</div>
									</div>
								{/if}

								<div
									class="flex items-start gap-2 rounded-lg border border-border bg-muted/30 p-3 text-xs text-muted-foreground"
								>
									<Info class="mt-0.5 size-3.5 shrink-0" />
									<p>
										Sylve takes over this uplink with a standard switch on the first boot. The
										machine has no network until its daemon starts, then the switch comes up with
										the addressing you chose.
									</p>
								</div>
								{/if}
							</div>
						{:else if step === 'summary'}
							<div class="space-y-4">
								<dl class="grid gap-3 sm:grid-cols-2">
									{#each review() as item (item.label)}
										<div
											class={cn(
												'flex items-start justify-between gap-2 rounded-lg border border-border bg-muted/40 p-3',
												item.wide && 'sm:col-span-2'
											)}
										>
											<div class="min-w-0 space-y-0.5">
												<dt class="text-xs text-muted-foreground">{item.label}</dt>
												<dd class="text-sm break-words">{item.value}</dd>
											</div>
											<button
												class="shrink-0 text-muted-foreground transition-colors hover:text-foreground"
												onclick={() => (step = item.step)}
												title={`Edit ${item.label}`}
												aria-label={`Edit ${item.label}`}
											>
												<Pencil class="size-3.5" />
											</button>
										</div>
									{/each}
								</dl>
								<p class="text-xs text-muted-foreground">
									The selected disks are repartitioned and everything on them is erased.
								</p>
							</div>
						{/if}
					</Card.Content>

					{#if step !== 'checks' || licenseAccepted}
						<Card.Footer class="justify-between">
							<Button
								variant="outline"
								onclick={back}
								disabled={stepIndex === 0 && step !== 'checks'}
							>
								Back
							</Button>
							<Button onclick={next} disabled={!canNext}>
								{stepIndex === FLOW.length - 1 ? 'Begin install' : 'Next'}
							</Button>
						</Card.Footer>
					{/if}

					{#if step === 'checks' && webURL && !licenseDeclined}
						<div class="animate-in fade-in px-6 duration-500">
							<div
								class="flex items-center gap-3 rounded-lg border border-border bg-muted/30 p-2.5"
							>
								<button
									type="button"
									class="shrink-0 rounded-md transition-opacity hover:opacity-90 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
									onclick={() => enlargeQR(webURL)}
									aria-label="Enlarge QR code"
									title="Enlarge QR code"
								>
									<img
										src={`/api/qr?text=${encodeURIComponent(webURL)}`}
										alt="Installer web address QR code"
										class="size-14 rounded-md border border-border bg-white p-1"
									/>
								</button>
								<div class="min-w-0">
									<p class="text-sm font-medium">Continue on another device</p>
									<p class="text-muted-foreground truncate text-xs">{webURL}</p>
									{#if access.password}
										<p class="text-xs">Password: <span class="font-normal">{access.password}</span></p>
									{/if}
								</div>
							</div>
						</div>
					{/if}
				</Card.Root>
			{/if}
		</main>
	</div>

	{#if access?.authenticated && liveNetworkOpen && step === 'checks' && licenseAccepted}
		<LiveNetworkDialog onApply={liveNetworkApplied} onClose={() => (liveNetworkOpen = false)} />
	{/if}

	{#if access?.authenticated && qrOpen}
		<Dialog.Root open onOpenChange={(open) => (qrOpen = open)}>
			<Dialog.Content class="gap-5 p-6 sm:max-w-sm" showCloseButton={false}>
				<Dialog.Header class="relative items-center text-center">
					<Dialog.Title class="flex items-center justify-center gap-2">
						<QrCode class="size-4" />
						Scan on another device
					</Dialog.Title>
					<Dialog.Close
						class="text-muted-foreground hover:text-foreground absolute top-1/2 right-0 -translate-y-1/2 transition-colors"
						aria-label="Close"
					>
						<X class="size-4" />
					</Dialog.Close>
				</Dialog.Header>
				<img
					src={`/api/qr?text=${encodeURIComponent(qrText)}`}
					alt={`QR code for ${qrText}`}
					class="mx-auto size-80 rounded-lg border border-border bg-white p-6"
				/>
				<p class="bg-muted mx-auto max-w-full rounded-md px-3 py-1.5 text-xs break-all">
					{qrText}
				</p>
				{#if access.password && qrText === webURL}
					<p class="text-center text-sm">Password: <span class="font-normal">{access.password}</span></p>
				{/if}
			</Dialog.Content>
		</Dialog.Root>
	{/if}

	<footer class="border-t border-border bg-background">
		<div class="mx-auto max-w-6xl px-6 py-2 text-center text-xs text-muted-foreground">
			<p class="flex items-center justify-center gap-1.5">
				<span>BSD-2-Clause · Made with</span>
				<Heart class="size-3 fill-current text-foreground" aria-hidden="true" />
				<span>by Alchemilla</span>
			</p>
		</div>
	</footer>
</div>
