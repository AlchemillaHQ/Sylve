export function stageDemoDownloaderUpload(
	_hostname: string,
	_name: string,
	_size: number,
	_storagePool = ''
): string {
	throw new Error('Demo uploads are not part of the production build.');
}
