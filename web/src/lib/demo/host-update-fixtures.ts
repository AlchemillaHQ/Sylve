import {
	SylveReleaseSchema,
	type HostUpdates,
	type HostUpdateJob
} from '$lib/types/system/updates';
import type { JailPackage } from '$lib/types/jail/packages';

const states = new Map<string, HostUpdates>();
const jobs = new Map<
	number,
	{ node: string; job: HostUpdateJob; finish: number; applied?: number }
>();
let operationID = 9000;
const now = () => new Date().toISOString();
const emptyPlan = () => ({ changes: [], download: '', space: '', digest: '' });

function initial(node: string): HostUpdates {
	const pkg = (
		name: string,
		version: string,
		kind: JailPackage['kind'] = 'application'
	): JailPackage => ({
		name,
		version,
		kind,
		origin: kind === 'base' || kind === 'kernel' ? `base/${name}` : `misc/${name}`,
		repository: kind === 'base' || kind === 'kernel' ? 'FreeBSD-base' : 'FreeBSD-ports',
		size: 5 << 20,
		automatic: false,
		locked: false,
		protected: kind !== 'application',
		available: '',
		advisories: []
	});
	const supported = node !== 'alia';
	const packages = [
		pkg('FreeBSD-runtime', '15.1p1', 'base'),
		pkg('FreeBSD-clibs', '15.1p1', 'base'),
		pkg('FreeBSD-kernel-generic', '15.1p1', 'kernel'),
		pkg('curl', '8.14.1')
	];
	const packaged = node === 'paul';
	if (packaged) packages.push({ ...pkg('sylve', '0.3.1'), origin: 'sysutils/sylve', locked: true });
	return {
		packages: supported ? packages : [],
		inventoryAt: now(),
		checkedAt: null,
		available: emptyPlan(),
		checkError: '',
		audit: { packages: [], databaseAt: null },
		auditAt: null,
		auditError: '',
		monitoring: true,
		intervalHours: 24,
		cronExpr: '',
		nextCheckAt: new Date(Date.now() + 86400000).toISOString(),
		lastAttemptAt: null,
		failureCount: 0,
		releaseChannel: 'rolling',
		release: null,
		releaseCheckedAt: null,
		releaseError: '',
		releaseAvailable: false,
		ready: supported,
		pkgbase: supported,
		reason: supported ? '' : 'host_updates_require_pkgbase',
		updatesChecked: false,
		auditStale: true,
		rebootRecommended: false,
		bootEnvironmentSupported: supported,
		sylve: {
			mode: packaged ? 'package' : 'standalone',
			reason: packaged ? 'sylve_package_managed' : '',
			version: '0.3.1',
			commit: 'a'.repeat(40),
			builtAt: '2026-10-19T12:00:00Z',
			channel: packaged ? 'package' : 'rolling',
			sha256: '1'.repeat(64),
			path: '/usr/local/sbin/sylve'
		},
		activeJob: null,
		history: []
	};
}

function transaction(view: HostUpdates, selected: string[]) {
	const changes = view.packages
		.filter((pkg) => !pkg.locked && (!selected.length || selected.includes(pkg.name)))
		.map((pkg) => ({
			action: 'upgrade',
			name: pkg.name,
			from: pkg.version,
			to:
				pkg.kind === 'base' || pkg.kind === 'kernel'
					? '15.1p2'
					: pkg.name === 'sylve'
						? '0.3.2'
						: '8.14.2',
			repository: pkg.repository
		}))
		.filter((change) => change.from !== change.to);
	return {
		changes,
		download: changes.length ? '24 MiB to be downloaded.' : '',
		space: '',
		digest: JSON.stringify(changes)
	};
}

function audit(view: HostUpdates) {
	view.audit = {
		databaseAt: now(),
		packages: view.packages
			.filter((pkg) => pkg.name === 'curl' && pkg.version === '8.14.1')
			.map((pkg) => ({
				name: pkg.name,
				version: pkg.version,
				advisories: [
					{
						id: 'demo-advisory',
						description: 'Demonstration finding — not a real advisory',
						url: 'https://vuxml.freebsd.org/',
						cves: ['CVE-2099-1234']
					}
				]
			}))
	};
	view.auditAt = now();
	view.auditStale = false;
	for (const pkg of view.packages)
		pkg.advisories = view.audit.packages.find((entry) => entry.name === pkg.name)?.advisories ?? [];
}

function release(view: HostUpdates) {
	const channel = view.releaseChannel;
	return {
		channel,
		tag: channel === 'rolling' ? 'tip' : 'v0.3.2',
		version: '0.3.2',
		commit: 'b'.repeat(40),
		sha256: '2'.repeat(64),
		assetId: 1,
		size: 75 << 20,
		url: `https://github.com/AlchemillaHQ/Sylve/releases/download/${channel === 'rolling' ? 'tip' : 'v0.3.2'}/sylve-amd64`,
		freebsd: '15.1-RELEASE',
		builtAt: now()
	};
}

function checkRelease(view: HostUpdates) {
	if (view.sylve.mode !== 'standalone') return;
	view.release = release(view);
	view.releaseCheckedAt = now();
	view.releaseError = '';
	view.releaseAvailable =
		view.release.sha256 !== view.sylve.sha256 &&
		(view.releaseChannel === 'rolling' || view.sylve.version !== view.release.version);
}

function settle(node: string, view: HostUpdates) {
	const entry = view.activeJob && jobs.get(view.activeJob.id);
	if (!entry || entry.node !== node || Date.now() < entry.finish) return;
	const job = entry.job;
	job.status = 'success';
	job.phase = 'completed';
	job.finishedAt = now();
	if (job.action === 'check' || job.action === 'audit') {
		if (view.pkgbase) audit(view);
		if (job.action === 'check') {
			view.available = transaction(view, []);
			view.checkedAt = now();
			view.updatesChecked = view.pkgbase;
			for (const pkg of view.packages)
				pkg.available = view.available.changes.find((change) => change.name === pkg.name)?.to ?? '';
			checkRelease(view);
		}
		view.lastAttemptAt = now();
	} else if (job.action === 'packages-apply' && job.plan) {
		for (const change of job.plan.transaction.changes) {
			const pkg = view.packages.find((pkg) => pkg.name === change.name);
			if (pkg) {
				pkg.version = change.to;
				pkg.available = '';
			}
			if (change.name.startsWith('FreeBSD-')) view.rebootRecommended = true;
		}
		view.available = emptyPlan();
		view.updatesChecked = false;
		audit(view);
		job.result = 'Demonstration package transaction completed. No real packages were changed.';
	} else if (job.action === 'sylve-apply' && job.plan?.release) {
		const target = job.plan.release;
		view.sylve = {
			...view.sylve,
			version: target.version,
			commit: target.commit,
			builtAt: target.builtAt,
			channel: target.channel,
			sha256: target.sha256
		};
		view.releaseAvailable = false;
		job.result =
			'Demonstration Sylve update completed. No executable was replaced and no service was restarted.';
	}
	view.history.unshift(job);
	view.activeJob = null;
}

export function demoHostUpdates(
	node: string,
	path: string,
	method: string,
	body: Record<string, unknown>,
	query: URLSearchParams
) {
	if (!states.has(node)) states.set(node, initial(node));
	const view = states.get(node)!;
	settle(node, view);
	const success = (data: unknown, status = 200) => ({
		status,
		ok: true,
		headers: {},
		data: { status: 'success', data }
	});
	const failure = (message: string) => ({
		status: 409,
		ok: false,
		headers: {},
		data: { status: 'error', message }
	});
	if (!path && method === 'GET') return success(view);
	if (path === 'sylve/release' && method === 'GET') {
		if (view.sylve.mode !== 'standalone') return failure(view.sylve.reason);
		checkRelease(view);
		return success(SylveReleaseSchema.parse(view));
	}
	if (path === 'monitoring' && method === 'PUT') {
		if (view.activeJob) return failure('host_update_in_progress');
		view.monitoring = body.enabled === true;
		view.cronExpr = typeof body.cronExpr === 'string' ? body.cronExpr.trim() : '';
		if (!view.cronExpr) view.intervalHours = Number(body.intervalHours);
		return success(view);
	}
	if (path === 'sylve/channel' && method === 'PUT') {
		if (view.activeJob) return failure('host_update_in_progress');
		if (body.channel !== 'rolling' && body.channel !== 'tagged')
			return failure('invalid_release_channel');
		if (view.releaseChannel !== body.channel) {
			view.releaseChannel = body.channel;
			view.release = null;
			view.releaseCheckedAt = null;
			view.releaseError = '';
			view.releaseAvailable = false;
		}
		return success(view);
	}
	const match = path.match(/^jobs\/(\d+)(\/logs)?$/);
	if (match) {
		const entry = jobs.get(Number(match[1]));
		if (!entry || entry.node !== node) return failure('host_update_plan_not_found');
		if (match[2]) {
			const text = `Demonstration only: ${entry.job.action} — ${entry.job.phase}. No host changes were made.\n`;
			const offset = Number(query.get('offset') || 0);
			return success({ text: text.slice(offset), next: text.length });
		}
		return success(entry.job);
	}
	if (!path.startsWith('actions/') || method !== 'POST') return failure('invalid_update_action');
	const action = path.slice('actions/'.length);
	if (
		![
			'check',
			'audit',
			'packages-preview',
			'packages-apply',
			'sylve-preview',
			'sylve-apply'
		].includes(action)
	)
		return failure('invalid_update_action');
	if (view.activeJob) return failure('host_update_in_progress');
	if ((action.startsWith('packages-') || action === 'audit') && !view.pkgbase)
		return failure('host_updates_require_pkgbase');
	let plan: HostUpdateJob['plan'] = null;
	if (action.endsWith('-apply')) {
		if (body.acknowledge !== true) return failure('host_update_acknowledgement_required');
		const preview = [...jobs.values()].find(
			(entry) =>
				entry.node === node &&
				entry.job.plan?.id === body.planId &&
				entry.job.action.endsWith('-preview')
		);
		if (!preview?.job.plan) return failure('host_update_plan_not_found');
		if (preview.applied) return success(jobs.get(preview.applied)!.job, 202);
		plan = preview.job.plan;
		if (plan.release && plan.release.channel !== view.releaseChannel)
			return failure('host_update_plan_stale');
		if (new Date(plan.expiresAt).getTime() <= Date.now()) return failure('host_update_plan_stale');
		preview.applied = operationID + 1;
	} else if (action.endsWith('-preview')) {
		plan = {
			id: `demo-${operationID + 1}`,
			kind: action.startsWith('sylve-') ? 'sylve' : 'packages',
			expiresAt: new Date(Date.now() + 600000).toISOString(),
			selection: [],
			transaction: emptyPlan(),
			release: null
		};
		if (plan.kind === 'sylve') {
			if (view.sylve.mode !== 'standalone') return failure('sylve_package_managed');
			const channel = view.releaseChannel;
			if (view.sylve.version === '0.3.2')
				return failure(
					channel === 'tagged' ? 'sylve_downgrade_forbidden' : 'sylve_already_current'
				);
			plan.release = release(view);
		} else {
			plan.selection = Array.isArray(body.packages) ? body.packages.map(String) : [];
			plan.transaction = transaction(view, plan.selection);
		}
	}
	const job: HostUpdateJob = {
		id: ++operationID,
		action,
		status: 'running',
		phase: action.endsWith('-apply') ? 'applying' : 'solving',
		planId: plan?.id ?? '',
		plan,
		channel: action.startsWith('sylve-') ? view.releaseChannel : '',
		actor: 'demo-admin',
		error: '',
		result: '',
		createdAt: now(),
		finishedAt: null
	};
	jobs.set(job.id, { node, job, finish: Date.now() + 2000 });
	view.activeJob = job;
	return success(job, 202);
}
