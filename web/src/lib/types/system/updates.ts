import { z } from 'zod/v4';
import {
	describeCodes,
	JailPackageSchema,
	packageReason,
	PackageAuditSchema,
	PackageChangeSchema
} from '$lib/types/jail/packages';

export const ReleaseCandidateSchema = z.object({
	channel: z.enum(['rolling', 'tagged']),
	tag: z.string(),
	version: z.string(),
	commit: z.string(),
	sha256: z.string(),
	assetId: z.number(),
	size: z.number(),
	url: z.string(),
	freebsd: z.string(),
	builtAt: z.string()
});
export const HostTransactionSchema = z.object({
	changes: z
		.array(PackageChangeSchema)
		.nullish()
		.transform((value) => value ?? []),
	download: z.string(),
	space: z.string(),
	digest: z.string()
});
export const HostUpdatePlanSchema = z.object({
	id: z.string(),
	kind: z.enum(['packages', 'sylve']),
	expiresAt: z.string(),
	selection: z
		.array(z.string())
		.nullish()
		.transform((value) => value ?? []),
	transaction: HostTransactionSchema,
	release: ReleaseCandidateSchema.nullable()
});
export const HostUpdateOperationSchema = z.object({
	id: z.number().int(),
	action: z.string(),
	status: z.string(),
	phase: z.string(),
	planId: z.string(),
	channel: z.string(),
	actor: z.string(),
	error: z.string(),
	result: z.string(),
	createdAt: z.string(),
	finishedAt: z.string().nullable()
});
export const HostUpdateJobSchema = HostUpdateOperationSchema.extend({
	plan: HostUpdatePlanSchema.nullable()
});
export const HostUpdateMonitoringSchema = z.object({
	monitoring: z.boolean(),
	intervalHours: z.number(),
	cronExpr: z.string().default(''),
	releaseChannel: z.enum(['rolling', 'tagged'])
});
export const HostUpdatesSchema = HostUpdateMonitoringSchema.extend({
	packages: z.array(JailPackageSchema),
	inventoryAt: z.string().nullable(),
	checkedAt: z.string().nullable(),
	available: HostTransactionSchema,
	checkError: z.string(),
	audit: PackageAuditSchema,
	auditAt: z.string().nullable(),
	auditError: z.string(),
	nextCheckAt: z.string().nullable(),
	lastAttemptAt: z.string().nullable(),
	failureCount: z.number(),
	release: ReleaseCandidateSchema.nullable(),
	releaseCheckedAt: z.string().nullable(),
	releaseError: z.string(),
	releaseAvailable: z.boolean(),
	ready: z.boolean(),
	pkgbase: z.boolean(),
	reason: z.string(),
	updatesChecked: z.boolean(),
	auditStale: z.boolean(),
	rebootRecommended: z.boolean(),
	bootEnvironmentSupported: z.boolean(),
	sylve: z.object({
		mode: z.string(),
		reason: z.string(),
		version: z.string(),
		commit: z.string(),
		builtAt: z.string().default(''),
		channel: z.string(),
		sha256: z.string(),
		path: z.string()
	}),
	activeJob: HostUpdateJobSchema.nullable(),
	history: z.array(HostUpdateOperationSchema)
});
export const SylveReleaseSchema = HostUpdatesSchema.pick({
	sylve: true,
	releaseChannel: true,
	release: true,
	releaseCheckedAt: true,
	releaseError: true,
	releaseAvailable: true
});
export type SylveRelease = z.infer<typeof SylveReleaseSchema>;
export type HostUpdates = z.infer<typeof HostUpdatesSchema>;
export type HostUpdateJob = z.infer<typeof HostUpdateJobSchema>;
export type HostUpdateAction =
	| 'check'
	| 'audit'
	| 'packages-preview'
	| 'packages-apply'
	| 'sylve-preview'
	| 'sylve-apply';
export interface HostUpdateRequest {
	planId?: string;
	packages?: string[];
	acknowledge?: boolean;
	bootEnvironment?: boolean;
}

export interface HostUpdateContext {
	readonly node: string;
	readonly view: HostUpdates | null;
	readonly error: string;
	readonly loading: boolean;
	readonly enabled: boolean;
	refresh: () => Promise<void>;
	run: (action: HostUpdateAction, request?: HostUpdateRequest) => Promise<void>;
	openOperation: (id: number) => void;
	openMonitoring: () => void;
}

export const hostUpdateActionLabels: Record<string, string> = {
	check: 'Check for Updates',
	audit: 'Package Audit',
	'packages-preview': 'Package Upgrade Preview',
	'packages-apply': 'Upgrade Packages',
	'sylve-check': 'Check Sylve Updates',
	'sylve-preview': 'Update Sylve',
	'sylve-apply': 'Update Sylve'
};

export function hostUpdateReason(reason = ''): string {
	const invalidRequest = 'The request was invalid. Refresh and try again.';
	const unverifiedRelease =
		'The published release metadata could not be verified. Try checking again later.';
	const messages: Record<string, string> = {
		host_base_metadata_invalid:
			'An installed base package has unexpected metadata. Repair the base system outside Sylve.',
		host_base_version_unsupported:
			'An installed base package has a version this updater does not recognise.',
		host_package_metadata_invalid:
			'Package metadata could not be verified. Try again, or repair the package database.',
		host_package_manager_ambiguous:
			'Both pkg and pkg-devel are installed. Remove one before using host updates.',
		host_boot_identity_unavailable: 'The host boot time could not be read. Nothing was installed.',
		host_update_interrupted:
			'Sylve restarted during this operation. Inspect installed packages and the log before retrying.',
		host_update_result_mismatch:
			'Installed packages do not match the approved changes. Inspect the log and the installed state.',
		host_update_plan_not_found: 'This preview is no longer available. Request a new preview.',
		host_update_plan_kind_changed:
			'This preview belongs to a different update type. Request a new preview.',
		invalid_package_selection:
			'A selected package is locked, duplicated or no longer installed. Refresh and try again.',
		no_package_changes: 'This preview has no package changes to install.',
		sylve_restart_unavailable:
			'The executable was updated, but Sylve cannot restart itself. Restart Sylve manually.',
		sylve_startup_identity_mismatch:
			'Sylve restarted with a different build than the one installed. Inspect the installation.',
		sylve_version_unknown:
			'The running Sylve version is unknown, so a Sylve package change cannot be verified.',
		release_binary_invalid:
			'The downloaded file is not a valid Sylve executable. Nothing was installed.',
		release_response_too_large: unverifiedRelease,
		release_version_mismatch: unverifiedRelease,
		untrusted_release_redirect: unverifiedRelease,
		untrusted_host_update_directory:
			'An update file or directory has unsafe ownership or permissions.',
		untrusted_host_update_file: 'An update file or directory has unsafe ownership or permissions.',
		invalid_update_action: invalidRequest,
		invalid_host_update_request: invalidRequest,
		invalid_update_operation: invalidRequest,
		host_update_request_too_large: invalidRequest,
		'context deadline exceeded': 'The host took too long to respond. Try again.',
		'record not found': 'This operation is no longer available.',
		invalid_update_cron: 'Enter a valid five-field cron expression: minute hour day month weekday.',
		invalid_update_interval: 'Choose a preset frequency or enter a valid cron expression.',
		invalid_release_channel: 'Choose Rolling Releases or Tagged Releases.',
		host_updates_require_pkgbase:
			'Host package updates require a registered pkgbase system. Update other installations outside Sylve.',
		host_updates_require_host:
			'Host updates are unavailable when Sylve itself is running inside a jail.',
		host_update_in_progress: 'Another host update operation is in progress.',
		host_update_cancelled: 'The operation was stopped. Nothing was installed.',
		host_update_not_cancellable: 'This operation has finished or cannot be stopped.',
		host_update_acknowledgement_required:
			'Confirm the maintenance impact before installing updates.',
		host_update_plan_stale: 'This preview is no longer current. Request a new preview.',
		host_update_plan_already_used: 'This preview has already been used.',
		package_archive_path_unavailable:
			'The package archive’s catalog metadata or cache path could not be verified. Review the command output.',
		package_archive_checksum_mismatch:
			'A package archive failed checksum verification. Nothing was installed. Check the repository and retry.',
		host_base_branch_changed:
			'The proposed update changes the FreeBSD release or branch. Release migrations are not supported by this updater.',
		host_base_branch_mixed:
			'Installed base packages belong to different release branches. Repair the installation outside Sylve.',
		host_base_selection_requires_all:
			'Base and kernel updates require a complete host update preview, including kernel modules.',
		host_base_repository_unavailable:
			'The repository used by the installed base system is not enabled.',
		host_base_repository_changed:
			'Base packages must come from the installed base system repository.',
		host_base_transaction_unsupported:
			'Removing or downgrading base packages requires manual maintenance.',
		host_base_package_locked:
			'A base or kernel-module package is locked. Resolve the lock manually before updating the base system; Sylve will not unlock it or install a partial base update.',
		host_package_requires_newer_release:
			'A package requires a newer FreeBSD release. Update the operating system outside this updater first.',
		host_package_abi_changed: 'A proposed package does not match this host’s FreeBSD ABI.',
		host_kernel_major_mismatch:
			'The installed base system and running kernel have different major versions. Finish that upgrade outside Sylve before continuing.',
		host_package_requires_newer_base:
			'A package requires a newer base/kernel build than is installed or included in this transaction. Update the base system first.',
		host_protected_package_change:
			'The transaction would remove or downgrade Sylve or the package manager.',
		standalone_sylve_package_conflict:
			'A package would replace the standalone Sylve executable. Resolve the installation-source conflict first.',
		sylve_package_managed:
			'Sylve is package-managed. Update it through host packages, not GitHub Releases. Locked packages remain package-managed.',
		packaged_sylve_modified:
			'The installed Sylve package has modified files. Remove the package before switching to standalone releases.',
		sylve_package_ownership_unknown:
			'Sylve’s package ownership could not be verified. Standalone updates are blocked.',
		sylve_package_ownership_conflict:
			'The running Sylve executable conflicts with an installed package. Resolve this before changing update sources.',
		sylve_standalone_path_required:
			'Standalone UI updates require the running binary at /usr/local/sbin/sylve.',
		sylve_executable_changed_restart_required:
			'The executable on disk changed. Restart Sylve and request a fresh preview.',
		sylve_downgrade_forbidden:
			'Downgrades are not allowed. A rolling build cannot switch to a tagged release of the same version.',
		sylve_already_current: 'This exact Sylve build is already installed.',
		sylve_build_unknown: 'The running Sylve version or build identity cannot be verified.',
		sylve_build_time_unknown:
			'Build date unknown. Same-version updates require a verifiable build date.',
		sylve_build_order_ambiguous:
			'These different builds have the same timestamp. Update order cannot be verified.',
		sylve_release_requires_newer_freebsd:
			'This release was built for a newer FreeBSD userland than this host.',
		release_publication_incomplete:
			'The release is being published or is missing required assets. Try again later.',
		release_fetch_failed: 'The release service could not be reached. Try checking again.',
		release_metadata_invalid:
			'The published release metadata could not be verified. Try checking again later.',
		release_changed_during_check:
			'The rolling release changed during the check. Request a fresh preview.',
		release_checksum_mismatch:
			'The download does not match the approved release checksum. Nothing was installed.',
		release_binary_identity_mismatch:
			'The downloaded binary does not match the expected build identity.',
		pkg_version_unsupported: 'Host updates require pkg 2.8.4 or newer in the 2.x series.',
		host_update_service_unavailable: 'Host update management is unavailable on this node.',
		boot_environment_unavailable: 'A ZFS boot environment cannot be created on this installation.',
		boot_environment_creation_failed:
			'The pre-update boot environment could not be created. No packages were installed.'
	};
	return describeCodes(
		reason,
		(code) =>
			messages[code] ??
			(packageReason(code, '') ||
				`The request could not be completed (${code || 'unknown error'}).`)
	);
}
