/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

export function getFileIcon(filename: string) {
	const ext = filename.split('.').pop()?.toLowerCase() || '';
	switch (ext) {
		case 'jpg':
		case 'jpeg':
		case 'png':
		case 'gif':
		case 'bmp':
		case 'svg':
			return 'icon-[lucide--image]';
		case 'mp4':
		case 'avi':
		case 'mkv':
		case 'mov':
		case 'wmv':
			return 'icon-[lucide--video]';
		case 'mp3':
		case 'wav':
		case 'flac':
		case 'ogg':
			return 'icon-[lucide--music]';
		case 'zip':
		case 'tar':
		case 'gz':
		case 'rar':
		case '7z':
			return 'icon-[lucide--archive]';
		case 'exe':
		case 'sh':
		case 'bin':
			return 'icon-[lucide--file-text]';
		case 'pdf':
		case 'doc':
		case 'docx':
		case 'txt':
		case 'md':
		case 'html':
		case 'css':
		case 'js':
		case 'ts':
		case 'json':
		case 'xml':
		case 'cshrc':
		case 'profile':
		default:
			return 'icon-[lucide--file-text]';
	}
}
