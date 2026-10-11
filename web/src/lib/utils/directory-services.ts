const directoryServiceErrorPrefix =
	/^(?:(?:invalid_directory_service_config|directory_service_(?:entry_not_found|entry_in_use|operation_in_progress|unavailable)):\s*)+/;

const fallback = 'The directory service operation failed. Check the configuration and try again.';
const encryptionRecovery =
	'Restore the matching directory services encryption key with the database backup.';
const authenticationFailure =
	'Kerberos authentication failed. Check the enrollment credentials, DNS and time synchronization.';

const messages = new Map([
	[
		"AD LDAP DNS SRV discovery failed; configure the node's DNS to use AD DNS",
		"Active Directory LDAP discovery failed. Check that the node's DNS servers are reachable and can resolve the domain's LDAP SRV records."
	],
	[
		'Kerberos DNS SRV discovery failed',
		"Kerberos discovery failed. Check that the node's DNS servers are reachable and can resolve the domain's Kerberos SRV records."
	],
	[
		'unable to verify domain controller clock offset',
		"The domain controller's clock offset could not be verified. Check the domain connection and the node's time synchronization."
	],
	[
		'unable to determine Samba version',
		'The Samba version could not be determined. Make sure Samba 4.16 or newer is installed.'
	],
	[
		'no domain controller in the selected AD site',
		'No domain controller was found in the selected AD site. Check the site setting and DNS availability.'
	],
	['AD machine keytab is empty', 'The AD machine keytab is empty.'],
	[
		'AD machine keytab must contain current machine, host, and CIFS keys for every configured hostname',
		'The AD machine keytab must contain current machine, host and CIFS keys for every configured hostname.'
	],
	[
		'AD machine keytab must contain current machine, host, and CIFS keys',
		'The AD machine keytab must contain current machine, host and CIFS keys.'
	],
	[
		'expected a version-2 Kerberos keytab (maximum 1 MiB)',
		'A version-2 Kerberos keytab of no more than 1 MiB is required.'
	],
	['truncated keytab entry', 'The keytab contains a truncated entry.'],
	['invalid keytab principal', 'The keytab contains an invalid principal.'],
	[
		'unsupported keytab principal characters',
		'The keytab contains unsupported principal characters.'
	],
	['invalid keytab principal component', 'The keytab contains an invalid principal component.'],
	['invalid keytab key entry', 'The keytab contains an invalid key entry.'],
	['combined system keytab exceeds 1 MiB', 'The combined system keytab exceeds the 1 MiB limit.'],
	[
		'system keytab backup must be a regular file',
		'The system keytab backup must be a regular file.'
	],
	[
		'Kerberos encryption key is missing; restore directory-services/keytab.key with the database backup',
		`The Kerberos encryption key is missing. ${encryptionRecovery}`
	],
	[
		'invalid Kerberos encryption key',
		`The Kerberos encryption key is invalid. ${encryptionRecovery}`
	],
	[
		'Kerberos encryption key must be a regular file',
		'The Kerberos encryption key must be stored in a regular file.'
	],
	[
		'invalid encrypted directory-services secret',
		`The stored directory service credentials are invalid. ${encryptionRecovery}`
	],
	[
		'cipher: message authentication failed',
		`The stored directory service credentials could not be decrypted. ${encryptionRecovery}`
	],
	[
		'An interrupted membership operation needs recovery. Retry Join/Leave, or remove local membership when the domain is unavailable.',
		'An interrupted domain membership operation needs recovery. Retry joining or leaving the domain. Remove local membership only if the domain is unavailable.'
	],
	[
		'join username and password are required; use user or user@REALM',
		'A join username and password are required. Use a domain username or include its realm.'
	],
	[
		'select either a keytab or username/password enrollment',
		'Select either a keytab or a username and password for enrollment.'
	],
	['invalid_directory_service_config', 'Directory service configuration is invalid.'],
	['directory_service_entry_not_found', 'The requested directory service entry was not found.'],
	['directory_service_entry_in_use', 'The directory service entry is still in use.'],
	[
		'directory_service_operation_in_progress',
		'Another directory service operation is in progress.'
	],
	['directory_service_unavailable', 'Directory services are unavailable.'],
	['directory_services_error', fallback],
	['invalid_directory_services_request', 'The directory service request is invalid.'],
	[
		'Expected a single JSON object with supported fields',
		'The directory service request is invalid. Refresh the page and try again.'
	],
	['record not found', 'The requested directory service entry was not found.'],
	[
		'Request failed',
		'The request could not be completed. Check the node connection and try again.'
	],
	['context deadline exceeded', 'The directory service operation timed out. Try again.'],
	['context canceled', 'The directory service operation was cancelled. Try again.']
]);

function sentence(message: string): string {
	const text = message
		.replace(/\s+/g, ' ')
		.replace(/;\s*/g, '. ')
		.replace(
			/(^|[.!?]\s+)([a-z])/g,
			(_, start: string, letter: string) => start + letter.toUpperCase()
		);
	return /[.!?]$/.test(text) ? text : `${text}.`;
}

function formatError(error: string): string {
	const message = error.trim().replace(directoryServiceErrorPrefix, '');
	const known = messages.get(message);
	if (known) return known;

	const partialJoin =
		'enrollment may have partially completed; check the connection or retry joining before changing configuration';
	if (message.endsWith(partialJoin)) {
		const cause = message.slice(0, -partialJoin.length).replace(/:\s*$/, '').trim();
		return `${cause ? formatError(cause) + ' ' : ''}Enrollment may have partially completed. Check the connection or retry joining before changing the configuration.`;
	}

	const skew = message.match(
		/^domain controller clock differs by (-?\d+) seconds; fix node time synchronization before joining$/
	);
	if (skew)
		return `The domain controller's clock differs from this node by ${skew[1].replace(/^-/, '')} seconds. Check the node's time synchronization.`;

	const wrapped = message.match(
		/^(read AD machine keytab|read Kerberos encryption key|cannot decrypt saved join password):\s*([\s\S]*)$/
	);
	if (wrapped) {
		const [, operation, cause] = wrapped;
		const fileError = /^(?:open|stat|lstat|read|chmod)\s/i.test(cause);
		const missing = fileError && /no such file or directory/i.test(cause);
		const denied = fileError && /permission denied/i.test(cause);
		if (operation === 'read Kerberos encryption key') {
			if (missing) return `The Kerberos encryption key is missing. ${encryptionRecovery}`;
			return "The Kerberos encryption key could not be read. Check the file's ownership and permissions.";
		}
		if (operation === 'read AD machine keytab' && missing)
			return 'The AD machine keytab is missing. Check the saved machine credentials and domain connection.';
		if (operation === 'read AD machine keytab' && denied)
			return "The AD machine keytab could not be read. Check the file's ownership and permissions.";
		if (
			operation === 'cannot decrypt saved join password' &&
			(cause === 'cipher: message authentication failed' ||
				cause === 'invalid encrypted directory-services secret')
		)
			return `The saved join credentials could not be decrypted. ${encryptionRecovery}`;
		const detail = formatError(cause);
		if (operation === 'cannot decrypt saved join password')
			return `The saved join credentials could not be decrypted. ${detail === fallback ? encryptionRecovery : detail}`;
		return `The AD machine keytab could not be read. ${detail === fallback ? 'Check the keytab file and domain connection.' : detail}`;
	}

	if (/^(?:invalid_samba_global_config|Samba configuration validation failed):/i.test(message))
		return 'The Samba configuration is invalid. Review the Samba settings and any extra global options that conflict with directory service settings.';
	if (/^failed to restart Samba service:/i.test(message))
		return 'Samba could not be restarted. Check the Samba service and its configuration.';
	if (/^refusing to replace non-regular file /i.test(message))
		return 'A directory service file is not a regular file. Check the file type and permissions before trying again.';
	if (/^refusing non-regular backup /i.test(message))
		return 'A directory service backup is not a regular file. Check the backup file before trying again.';

	const command = message.match(/^(net|wbinfo|kinit|testparm):/i)?.[1].toLowerCase();
	if (command) {
		if (/context canceled/i.test(message))
			return 'The directory service operation was cancelled. Try again.';
		if (/context deadline exceeded|timed? out|timeout/i.test(message))
			return 'The directory service operation timed out. Check the domain connection and try again.';
		if (
			/NT_STATUS_(?:TRUSTED_RELATIONSHIP_FAILURE|TRUSTED_DOMAIN_FAILURE|NO_TRUST_SAM_ACCOUNT)/i.test(
				message
			)
		)
			return "The domain trust check failed. Check the node's machine account and domain connection.";
		if (/NT_STATUS_ACCOUNT_DISABLED|WERR_ACCOUNT_DISABLED/i.test(message))
			return 'A domain account used by this node is disabled. Check the account status in Active Directory.';
		if (
			/NT_STATUS_LOGON_FAILURE|WERR_LOGON_FAILURE|wrong password|preauthentication failed/i.test(
				message
			)
		)
			return 'Domain authentication failed. Check the credentials and the domain account status.';
		if (/NT_STATUS_ACCESS_DENIED|WERR_ACCESS_DENIED|insufficient access rights/i.test(message))
			return 'The directory service operation was denied. Check the account permissions.';
		if (
			/NT_STATUS_(?:NO_LOGON_SERVERS|DOMAIN_CONTROLLER_NOT_FOUND)|connection refused|network is unreachable|no route to host/i.test(
				message
			)
		)
			return "The domain controller could not be reached. Check the node's DNS servers and domain-controller connectivity.";
		if (/clock skew/i.test(message))
			return "Kerberos authentication failed because the clocks are too far apart. Check the node's time synchronization.";
		if (/fork\/exec .*no such file or directory/i.test(message))
			return 'A required directory service command is unavailable. Check that Samba and Kerberos are installed.';
		switch (command) {
			case 'kinit':
				return authenticationFailure;
			case 'wbinfo':
				return 'The Winbind directory service check failed. Check that Samba and Winbind are running and can reach the domain controller.';
			case 'testparm':
				return 'The Samba configuration could not be validated. Check the Samba settings.';
			default:
				return 'The Samba directory service operation failed. Check the domain connection and Samba configuration.';
		}
	}

	// Keep prose validation errors, but never display unknown codes or raw command output.
	if (
		/^[A-Za-z]/.test(message) &&
		message !== message.toUpperCase() &&
		!/[_:<>{}[\]]/.test(message) &&
		!message.includes('/') &&
		!message.includes('\\') &&
		!/[\p{Cc}]/u.test(message) &&
		!/\b(?:exit status|signal|stack trace)\b/i.test(message) &&
		/\s/.test(message)
	)
		return sentence(message);
	return fallback;
}

export function formatDirectoryServiceError(error: string): string {
	if (!error.trim()) return '';
	// Keep command output together while retaining distinct joined service errors.
	const errors: string[] = [];
	for (const line of error.trim().split(/\r?\n/)) {
		const text = line.trim();
		if (
			!errors.length ||
			directoryServiceErrorPrefix.test(text) ||
			messages.has(text) ||
			/^(?:read AD machine keytab|read Kerberos encryption key|cannot decrypt saved join password):/.test(
				text
			)
		)
			errors.push(line);
		else errors[errors.length - 1] += '\n' + line;
	}
	return [...new Set(errors.map(formatError))].join(' ');
}
