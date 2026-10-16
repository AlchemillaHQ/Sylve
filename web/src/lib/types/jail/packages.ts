import { z } from 'zod/v4';

export const PackageScopeSchema = z.enum(['applications', 'system']).default('applications');
export const PackageKindSchema = z.enum([
	'application',
	'base',
	'package-manager',
	'kernel',
	'protected'
]);
export const PackageSystemSchema = z.object({
	release: z.string(),
	abi: z.string(),
	osVersion: z.number().int(),
	hostOSVersion: z.number().int(),
	repository: z.string()
});

export const PackageAdvisorySchema = z.object({
	id: z.string(),
	description: z.string(),
	url: z.string(),
	cves: z
		.array(z.string())
		.nullish()
		.transform((v) => v ?? [])
});
export const JailPackageSchema = z.object({
	name: z.string(),
	version: z.string(),
	origin: z.string(),
	repository: z.string(),
	size: z.number(),
	automatic: z.boolean(),
	locked: z.boolean(),
	protected: z.boolean(),
	kind: PackageKindSchema.default('application'),
	available: z.string(),
	advisories: z.array(PackageAdvisorySchema)
});
export const PackageAuditSchema = z.object({
	databaseAt: z.string().nullish(),
	packages: z.array(
		z.object({
			name: z.string(),
			version: z.string(),
			advisories: z.array(PackageAdvisorySchema)
		})
	)
});
export const PackagePlanSchema = z.object({
	id: z.string(),
	expiresAt: z.string(),
	selection: z.array(z.string()),
	scope: PackageScopeSchema,
	system: PackageSystemSchema.nullable().default(null),
	transaction: z.object({
		changes: z.array(
			z.object({
				action: z.string(),
				name: z.string(),
				from: z.string(),
				to: z.string(),
				repository: z.string()
			})
		),
		download: z.string(),
		space: z.string(),
		digest: z.string()
	})
});
export const PackageMonitoringSchema = z.object({
	enabled: z.boolean(),
	intervalHours: z.number(),
	nextAuditAt: z.string().nullable(),
	lastAttemptAt: z.string().nullable(),
	lastSuccessAt: z.string().nullable(),
	failureCount: z.number(),
	skipReason: z.string()
});
export const PackageJobSchema = z.object({
	taskId: z.number(),
	operationId: z.string(),
	action: z.string(),
	scope: PackageScopeSchema,
	phase: z.string(),
	status: z.string(),
	error: z.string(),
	result: z.string(),
	snapshotId: z.number().nullable(),
	plan: PackagePlanSchema.nullable(),
	audit: PackageAuditSchema.nullable(),
	createdAt: z.string(),
	finishedAt: z.string().nullable()
});
export const PackageViewSchema = z.object({
	jailId: z.number().int(),
	guid: z.string(),
	packages: z.array(JailPackageSchema),
	ready: z.boolean(),
	offline: z.boolean(),
	reason: z.string(),
	snapshotSupported: z.boolean(),
	inventoryAt: z.string().nullable(),
	checkedAt: z.string().nullable(),
	updatesChecked: z.boolean(),
	auditAt: z.string().nullable(),
	auditStale: z.boolean(),
	auditError: z.string(),
	checkError: z.string(),
	pkgbase: z.boolean().default(false),
	systemUpdatesChecked: z.boolean().default(false),
	systemCheckedAt: z.string().nullable().default(null),
	systemCheckError: z.string().default(''),
	audit: PackageAuditSchema,
	monitoring: PackageMonitoringSchema,
	activeJob: PackageJobSchema.nullable()
});
export const PackageLogSchema = z.object({ text: z.string(), next: z.number() });
export const PackageDetailsSchema = z.object({
	package: JailPackageSchema,
	dependencies: z.array(
		z.object({
			name: z.string(),
			version: z.string(),
			origin: z.string()
		})
	),
	dependenciesAvailable: z.boolean()
});

export type JailPackage = z.infer<typeof JailPackageSchema>;
export type PackageAudit = z.infer<typeof PackageAuditSchema>;
export type PackageJob = z.infer<typeof PackageJobSchema>;
export type PackageView = z.infer<typeof PackageViewSchema>;
export type PackageMonitoring = z.infer<typeof PackageMonitoringSchema>;
export type PackageDetails = z.infer<typeof PackageDetailsSchema>;
export type PackageAction = 'audit' | 'check-updates' | 'upgrade/preview' | 'upgrade';
export type PackageScope = z.infer<typeof PackageScopeSchema>;
export interface PackageRequest {
	packages?: string[];
	all?: boolean;
	planId?: string;
	snapshot?: boolean;
	acknowledge?: boolean;
	scope?: PackageScope;
}

export function packageKindLabel(kind: JailPackage['kind']): string {
	return {
		application: 'Application',
		base: 'Base system',
		'package-manager': 'Package manager',
		kernel: 'Kernel / boot',
		protected: 'Protected package'
	}[kind];
}

export function safeAdvisoryURL(value: string): string | null {
	try {
		const url = new URL(value);
		return ['https:', 'http:'].includes(url.protocol) && !url.username && !url.password
			? url.href
			: null;
	} catch {
		return null;
	}
}
export function packageReason(
	reason = '',
	fallback = 'The package operation could not be completed.'
): string {
	const labels: Record<string, string> = {
		no_eligible_packages: 'No eligible packages to upgrade',
		no_eligible_package_changes: 'No package changes to apply',
		package_management_unsupported_jail_type: 'Packages are available only for FreeBSD jails',
		package_service_unavailable: 'Package management is unavailable on this node',
		jail_not_running: 'Start the jail before applying upgrades',
		jail_creation_in_progress: 'Wait for the jail to finish being created',
		jail_operation_in_progress: 'Another jail operation is in progress',
		jail_runtime_unavailable: 'The jail’s running state could not be checked',
		jail_runtime_identity_changed: 'The jail’s running state changed. Refresh and try again.',
		jail_dataset_identity_changed: 'The jail’s filesystem changed. Refresh and try again.',
		jail_dataset_identity_unavailable: 'The jail’s filesystem could not be verified',
		package_root_unavailable: 'The jail’s filesystem is unavailable',
		package_root_not_mounted: 'Jail filesystem is not mounted — recorded results',
		package_root_mount_changed: 'The jail’s mounted filesystem changed. Refresh and try again.',
		package_mount_state_changed: 'The jail’s mounted filesystems changed. Refresh and try again.',
		package_offline_repository_unsupported: 'Offline checks require HTTP(S) or local repositories',
		package_operation_in_progress: 'Operation in progress',
		pkg_static_not_found: 'pkg-static unavailable',
		pkg_static_not_trusted: 'The host package tool could not be verified',
		pkg_version_unsupported: 'Requires host pkg-static 2.8.4',
		package_database_unavailable: 'Package database unavailable',
		package_audit_failed: 'The vulnerability audit failed. Check the command output for details.',
		package_audit_database_unavailable: 'The vulnerability advisory database is unavailable',
		package_inventory_changed_during_audit:
			'Installed packages changed during the audit. Run it again.',
		package_inventory_changed: 'Installed packages changed. Create a fresh upgrade preview.',
		package_catalog_unavailable: 'A repository catalog is unavailable. Check for updates again.',
		package_archive_path_unavailable:
			'The package archive’s repository path could not be verified. Check the command output for details.',
		application_repositories_unavailable: 'No application package repositories are available',
		package_command_unavailable: 'The package command could not be started',
		package_command_failed: 'The package command failed. Check the command output for details.',
		package_output_truncated: 'The package command produced too much output to verify safely',
		package_plan_not_found: 'This upgrade preview is unavailable. Create a new preview.',
		package_plan_stale: 'This upgrade preview has expired or changed. Create a new preview.',
		package_plan_changed: 'The proposed package changes no longer match. Create a fresh preview.',
		package_plan_already_used: 'This upgrade preview has already been applied',
		package_plan_scope_changed:
			'This preview belongs to a different upgrade type. Create a new preview.',
		invalid_package_scope: 'Choose application updates or jail-system updates',
		package_system_not_pkgbase: 'This jail does not have a registered pkgbase system',
		package_system_release_unsupported:
			'This FreeBSD userland release is not supported for managed system updates',
		package_system_release_changed:
			'System updates must stay within the jail’s current FreeBSD release',
		package_system_metadata_invalid: 'The jail’s base package metadata could not be verified',
		package_system_abi_changed: 'A base package has a different ABI from this jail',
		package_system_host_version_unavailable: 'The host kernel version could not be checked',
		package_system_host_too_old:
			'Upgrade the host first; these base packages require a newer kernel',
		package_system_repository_unavailable:
			'The jail’s base-system repository is unavailable or disabled',
		package_system_repository_mixed:
			'The base packages or proposed changes use inconsistent repositories',
		package_system_repository_ambiguous: 'More than one base-system repository matches this jail',
		package_system_catalog_incomplete:
			'The selected repository does not contain all of this jail’s base packages',
		package_system_package_locked:
			'Unlock the affected base packages manually before updating the jail system',
		package_system_transaction_unsupported:
			'This system preview includes removals, downgrades, or packages outside the base userland',
		package_system_selection_required:
			'Use Updates → Jail system to preview the complete base userland',
		package_system_acknowledgement_required:
			'Acknowledge the jail-system changes before applying this preview',
		package_changes_acknowledgement_required:
			'Acknowledge the listed removals and downgrades first',
		package_not_upgradeable: 'Locked and protected packages cannot be upgraded here',
		protected_package_change: 'The preview includes a protected package and cannot be applied here',
		protected_or_locked_package_change: 'The preview includes a locked or protected package',
		kernel_package_change_unsupported: 'Kernel and boot package changes are not supported here',
		package_snapshot_coverage_incomplete: 'A snapshot cannot cover this jail’s full storage layout',
		package_snapshot_failed: 'The pre-upgrade snapshot failed. No upgrade was started.',
		package_upgrade_failed: 'The upgrade failed. Check the command output and installed packages.',
		additional_package_transaction_unsupported:
			'The upgrade requested additional changes. Create a fresh preview.',
		package_job_not_found: 'This package operation is no longer available',
		package_not_found: 'This package is no longer installed',
		package_not_installed: 'A selected package is no longer installed',
		package_selection_required:
			'Select packages to preview, or use Updates → Applications → Preview all upgrades',
		invalid_package_name: 'The selected package name is invalid',
		invalid_package_audit_interval: 'Choose Never, every 6 hours, every 12 hours, or Daily',
		invalid_package_inventory: 'The installed package list could not be read',
		invalid_package_dependencies: 'Package dependencies could not be read',
		package_monitor_metadata_write_failed:
			'The monitoring settings could not be saved completely. Refresh and check them.',
		lifecycle_task_in_progress: 'Another jail operation is in progress',
		replication_lease_not_owned: 'Jail owned by another node',
		package_shared_system_mount_unsupported: 'Shared system filesystem unsupported',
		package_system_path_symlink_unsupported: 'Shared system path unsupported'
	};
	return labels[reason.split(':', 1)[0].trim()] ?? fallback;
}
