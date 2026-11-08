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
export const PackageChangeSchema = z.object({
	action: z.string(),
	name: z.string(),
	from: z.string(),
	to: z.string(),
	repository: z.string()
});
export const PackagePlanSchema = z.object({
	id: z.string(),
	expiresAt: z.string(),
	selection: z.array(z.string()),
	scope: PackageScopeSchema,
	system: PackageSystemSchema.nullable().default(null),
	transaction: z.object({
		changes: z.array(PackageChangeSchema),
		download: z.string(),
		space: z.string(),
		digest: z.string()
	})
});
export const PackageMonitoringSchema = z.object({
	enabled: z.boolean(),
	intervalHours: z.number(),
	cronExpr: z.string().default(''),
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
	sharedSystem: z.boolean().default(false),
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
// Joined Go errors arrive one per line, each as "code: detail".
export function describeCodes(reason: string, describe: (code: string) => string): string {
	const codes = reason
		.split('\n')
		.map((line) => line.split(':', 1)[0].trim())
		.filter(Boolean);
	return [...new Set((codes.length ? codes : ['']).map(describe))].join(' ');
}

export function packageReason(reason = '', fallback?: string): string {
	const unverifiedPlan =
		'pkg proposed a transaction that could not be verified. Nothing was installed. Create a fresh preview.';
	const unverifiedData =
		'Repository or package data could not be verified. Check for updates again.';
	const invalidRequest = 'The request was invalid. Refresh and try again.';
	const labels: Record<string, string> = {
		no_eligible_packages: 'No eligible packages to upgrade',
		no_eligible_package_changes: 'No package changes to apply',
		package_management_unsupported_jail_type: 'Packages are available only for FreeBSD jails',
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
		package_operation_cancelled: 'The operation was stopped. Nothing was installed.',
		package_operation_not_cancellable: 'This operation has finished or cannot be stopped',
		pkg_static_not_found: 'pkg-static unavailable',
		pkg_static_not_trusted: 'The host package tool could not be verified',
		pkg_version_unsupported: 'Requires pkg 2.8.4 or newer in the 2.x series on the host',
		package_database_unavailable: 'Package database unavailable',
		package_audit_failed: 'The vulnerability audit failed. Check the command output for details.',
		package_audit_database_unavailable: 'The vulnerability advisory database is unavailable',
		package_inventory_changed_during_audit:
			'Installed packages changed during the audit. Run it again.',
		package_inventory_changed: 'Installed packages changed. Create a fresh upgrade preview.',
		package_catalog_unavailable: 'A repository catalog is unavailable. Check for updates again.',
		package_archive_path_unavailable:
			'The package archive’s repository path could not be verified. Check the command output for details.',
		package_archive_checksum_mismatch:
			'A package archive failed checksum verification. Nothing was installed. Check the repository and retry.',
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
		invalid_package_audit_interval: 'Choose a preset frequency or enter a valid cron expression.',
		invalid_package_audit_cron:
			'Enter a valid five-field cron expression: minute hour day month weekday.',
		invalid_package_inventory: 'The installed package list could not be read',
		invalid_package_dependencies: 'Package dependencies could not be read',
		package_monitor_metadata_write_failed:
			'The monitoring settings could not be saved completely. Refresh and check them.',
		lifecycle_task_in_progress: 'Another jail operation is in progress',
		replication_lease_not_owned: 'Jail owned by another node',
		package_shared_system_mount_unsupported:
			'Upgrades are unavailable while a shared filesystem is mounted over a system path in this jail',
		package_system_path_symlink_unsupported: 'Shared system path unsupported',
		jail_dataset_mountpoint_not_usable: 'The jail’s filesystem mountpoint is unavailable',
		invalid_jail_runtime_state: 'The jail’s running state could not be checked',
		package_operation_interrupted:
			'Sylve restarted during this upgrade. Inspect installed packages and the command output before trying again.',
		package_offline_command_unsupported: 'Start the jail to run this operation',
		sylve_shutting_down: 'Sylve is shutting down. Try again after it restarts.',
		duplicate_package_selection: 'A package was selected more than once',
		too_many_packages: 'Too many packages selected. Preview all upgrades instead.',
		package_repository_ambiguous:
			'A package is offered by more than one repository. Disable the duplicate repository and try again.',
		package_file_on_shared_mount_unsupported:
			'A package would install files onto a shared filesystem. Nothing was installed.',
		package_file_parent_symlink_unsupported:
			'A package would install files through a symbolic link. Nothing was installed.',
		invalid_package_file_path: 'A package contains an invalid file path. Nothing was installed.',
		unsupported_package_plan: unverifiedPlan,
		incomplete_package_plan: unverifiedPlan,
		duplicate_package_plan_action: unverifiedPlan,
		empty_package_confirmation: unverifiedPlan,
		unexpected_package_prompt: unverifiedPlan,
		package_transaction_not_confirmed: unverifiedPlan,
		package_confirmation_output_too_large: unverifiedPlan,
		invalid_package_catalog: unverifiedData,
		invalid_package_repository: unverifiedData,
		invalid_package_version: unverifiedData,
		invalid_package_advisory: unverifiedData,
		invalid_package_audit_report: unverifiedData,
		protected_or_invalid_package_archive: unverifiedData,
		package_archive_checksum_invalid: unverifiedData,
		package_archive_checksum_unsupported: unverifiedData,
		invalid_package_request: invalidRequest,
		invalid_package_action: invalidRequest,
		invalid_package_operation: invalidRequest,
		invalid_package_target: invalidRequest,
		invalid_package_task: invalidRequest,
		invalid_log_offset: invalidRequest,
		invalid_ct_id: invalidRequest
	};
	return describeCodes(
		reason,
		(code) =>
			labels[code] ??
			fallback ??
			`The package operation could not be completed (${code || 'unknown error'}).`
	);
}
