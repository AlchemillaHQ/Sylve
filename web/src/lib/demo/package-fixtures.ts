import type { JailPackage, PackageJob, PackageView } from '$lib/types/jail/packages';

const states = new Map<string, PackageView>();
const jobs = new Map<
	number,
	{ job: PackageJob; finish: number; selection: string[]; all: boolean }
>();
let taskID = 8000;
const currentTime = () => new Date().toISOString();

function initial(ctId: number, jailId: number, running: boolean): PackageView {
	const advisory = {
		id: '11111111-2222-3333-4444-555555555555',
		description: 'Demonstration advisory — fixture only',
		url: 'https://vuxml.freebsd.org/',
		cves: ['CVE-2099-1234']
	};
	const pkg = (
		name: string,
		version: string,
		available: string,
		extra: Partial<JailPackage> = {}
	): JailPackage => ({
		name,
		version,
		available,
		origin: `misc/${name}`,
		repository: 'FreeBSD-ports',
		size: 5 << 20,
		automatic: false,
		locked: false,
		protected: false,
		kind: 'application',
		advisories: [],
		...extra
	});
	const vulnerable = ctId !== 201;
	const packages = [
		pkg('curl', '8.14.1', '8.15.0', { advisories: vulnerable ? [advisory] : [] }),
		pkg('git', '2.50.0', '2.51.0'),
		pkg('ca_root_nss', '3.112', ''),
		pkg('libnghttp2', '1.65.0', '', { automatic: true }),
		pkg('postgresql17-client', '17.5', '', { locked: true }),
		pkg('FreeBSD-runtime', '15.1p1', '15.1p2', {
			protected: true,
			kind: 'base',
			origin: 'base/FreeBSD-runtime',
			repository: 'FreeBSD-base'
		}),
		pkg('FreeBSD-clibs', '15.1', '15.1p1', {
			protected: true,
			kind: 'base',
			origin: 'base/FreeBSD-clibs',
			repository: 'FreeBSD-base'
		}),
		pkg('pkg', '2.8.4', '', { protected: true, kind: 'package-manager' })
	];
	return {
		jailId,
		guid: `demo-jail-${ctId}`,
		packages,
		ready: true,
		offline: !running,
		reason: '',
		snapshotSupported: running,
		inventoryAt: currentTime(),
		checkedAt: currentTime(),
		updatesChecked: true,
		auditAt: ctId === 210 ? '2026-01-01T00:00:00Z' : currentTime(),
		auditStale: ctId === 210,
		auditError: ctId === 210 ? 'Last audit feed fetch failed' : '',
		checkError: '',
		pkgbase: true,
		systemUpdatesChecked: false,
		systemCheckedAt: null,
		systemCheckError: '',
		audit: {
			packages: vulnerable ? [{ name: 'curl', version: '8.14.1', advisories: [advisory] }] : []
		},
		monitoring: {
			enabled: false,
			intervalHours: 24,
			nextAuditAt: null,
			lastAttemptAt: null,
			lastSuccessAt: null,
			failureCount: 0,
			skipReason: ''
		},
		activeJob: null
	};
}

function settle(view: PackageView) {
	if (!view.activeJob) return;
	const entry = jobs.get(view.activeJob.taskId);
	if (!entry || Date.now() < entry.finish) return;
	const job = entry.job;
	job.status = 'success';
	job.phase = 'completed';
	job.finishedAt = currentTime();
	if (job.action === 'pkg-upgrade') {
		for (const pkg of view.packages)
			if (entry.selection.includes(pkg.name) && pkg.available) {
				pkg.version = pkg.available;
				pkg.available = '';
				pkg.advisories = [];
			}
		view.audit = {
			packages: view.packages
				.filter((p) => p.advisories.length)
				.map((p) => ({ name: p.name, version: p.version, advisories: p.advisories }))
		};
		job.result =
			'Packages upgraded. Review configuration messages and restart affected services as needed.';
		view.updatesChecked = false;
		view.systemUpdatesChecked = false;
	}
	if (job.action === 'pkg-audit' || job.action === 'pkg-upgrade') {
		job.audit = structuredClone(view.audit);
		view.auditAt = currentTime();
		view.auditStale = false;
		view.auditError = '';
		job.result ||= job.audit.packages.length
			? 'Known vulnerabilities found'
			: 'No known vulnerabilities found';
	}
	if (job.action === 'pkg-check-updates' || (job.action === 'pkg-preview' && entry.all)) {
		if (job.scope === 'system') {
			view.systemCheckedAt = currentTime();
			view.systemUpdatesChecked = true;
		} else {
			view.checkedAt = currentTime();
			view.updatesChecked = true;
		}
		if (job.action === 'pkg-check-updates') job.result = 'Package update check completed';
	}
	view.activeJob = null;
	view.ready = true;
	view.reason = '';
}

export function demoPackages(
	node: string,
	ctId: number,
	jailId: number,
	running: boolean,
	path: string,
	method: string,
	body: Record<string, unknown>,
	query: URLSearchParams
) {
	const key = `${node}:${ctId}`;
	if (!states.has(key)) states.set(key, initial(ctId, jailId, running));
	const view = states.get(key)!;
	settle(view);
	view.offline = !running;
	view.snapshotSupported = running;
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
	if (!path || (path === 'audit' && method === 'GET')) return success(view);
	if (path === 'monitoring') {
		if (method === 'PUT') {
			view.monitoring.enabled = body.enabled === true;
			view.monitoring.intervalHours = Number(body.intervalHours);
			view.monitoring.nextAuditAt = body.enabled ? currentTime() : null;
		}
		return success(view.monitoring);
	}
	if (path.startsWith('installed/')) {
		const pkg = view.packages.find((p) => p.name === decodeURIComponent(path.slice(10)));
		return pkg
			? success({ package: pkg, dependencies: [], dependenciesAvailable: view.ready })
			: failure('package_not_found');
	}
	const match = path.match(/^jobs\/(\d+)(\/logs)?$/);
	if (match) {
		const entry = jobs.get(Number(match[1]));
		if (!entry || entry.job.operationId !== `${key}:${entry.job.taskId}`)
			return failure('package_job_not_found');
		if (match[2]) {
			const logs = `${entry.job.action}: ${entry.job.phase}\n`;
			const offset = Number(query.get('offset') || 0);
			return success({ text: logs.slice(offset), next: logs.length });
		}
		return success(entry.job);
	}
	if (method !== 'POST') return failure('invalid_package_action');
	const scope = body.scope === 'system' ? 'system' : 'applications';
	if (body.scope && body.scope !== 'system' && body.scope !== 'applications')
		return failure('invalid_package_scope');
	if (path === 'audit' && scope !== 'applications') return failure('invalid_package_scope');
	if (scope === 'system' && !view.pkgbase) return failure('package_system_not_pkgbase');
	if (path === 'upgrade' && !running) return failure('jail_not_running');
	if (view.activeJob) return failure('package_operation_in_progress');
	const eligible = view.packages
		.filter((p) => !p.locked && (scope === 'system' ? p.kind === 'base' : !p.protected))
		.map((p) => p.name);
	let selection = Array.isArray(body.packages) ? body.packages.map(String) : [];
	if (body.all) selection = eligible;
	const job: PackageJob = {
		taskId: ++taskID,
		operationId: `${key}:${taskID}`,
		action: `pkg-${path === 'upgrade/preview' ? 'preview' : path}`,
		scope,
		phase: 'running',
		status: 'running',
		result: '',
		error: '',
		snapshotId: body.snapshot ? 901 : null,
		plan: null,
		audit: null,
		createdAt: currentTime(),
		finishedAt: null
	};
	if (path === 'upgrade/preview') {
		if (scope === 'system' && body.all !== true)
			return failure('package_system_selection_required');
		if (selection.some((name) => !eligible.includes(name)))
			return failure('package_not_upgradeable');
		if (selection.length === 0) {
			job.result = 'No eligible application packages to upgrade';
		} else
			job.plan = {
				id: `plan-${taskID}`,
				expiresAt: new Date(Date.now() + 600000).toISOString(),
				selection,
				scope,
				system:
					scope === 'system'
						? {
								release: '15.1',
								abi: 'FreeBSD:15:amd64',
								osVersion: 1501000,
								hostOSVersion: 1600027,
								repository: 'FreeBSD-base'
							}
						: null,
				transaction: {
					changes: view.packages
						.filter((p) => selection.includes(p.name) && p.available)
						.map((p) => ({
							action: 'upgrade',
							name: p.name,
							from: p.version,
							to: p.available,
							repository: p.repository
						})),
					download: '12 MiB to be downloaded.',
					space: '',
					digest: `fixture-${taskID}`
				}
			};
	}
	if (path === 'upgrade') {
		const preview = [...jobs.values()].find((entry) => entry.job.plan?.id === body.planId);
		if (!preview?.job.plan) return failure('package_plan_not_found');
		if (body.scope && body.scope !== preview.job.scope)
			return failure('package_plan_scope_changed');
		if (preview.job.scope === 'system' && body.acknowledge !== true)
			return failure('package_system_acknowledgement_required');
		selection = preview.selection;
		job.plan = preview.job.plan;
		job.scope = preview.job.scope;
	}
	jobs.set(taskID, { job, finish: Date.now() + 3500, selection, all: body.all === true });
	view.activeJob = job;
	view.ready = false;
	view.reason = 'package_operation_in_progress';
	return success(job, 202);
}
