/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { getIcon, loadIcon } from '@iconify/svelte';
import { Archive, FileText, ImageIcon, Music, Video } from 'lucide-svelte';

export const iconCache: Record<string, string> = {};
const icons = [
	'carbon:ibm-cloud-vpc-block-storage-snapshots',
	'material-symbols:files',
	'carbon:volume-block-storage',
	'bi:hdd-stack-fill',
	'mdi:harddisk',
	'mdi:create-new-folder-outline',
	'mdi:delete-outline',
	'bi:nvme',
	'icon-park-outline:ssd',
	'ant-design:partition-outlined',
	'mdi:magnet',
	'mdi:download',
	'mdi:internet',
	'mdi:file',
	'mingcute:file-fill',
	'line-md:downloading-loop',
	'lets-icons:check-fill',
	'material-symbols-light:private-connectivity-outline',
	'material-symbols:public',
	'clarity:network-switch-line',
	'ic:baseline-loop',
	'mdi:ethernet',
	'eos-icons:three-dots-loading',
	'wpf:connected',
	'tdesign:cd-filled',
	'clarity:hard-disk-solid',
	'file-icons:openzfs',
	'carbon:volume-block-storage',
	'mdi:file-plus',
	'mdi:rename',
	'lets-icons:check-fill',
	'gridicons:cross-circle',
	'fluent-mdl2:party-leader',
    'mdi:forever'
];

export async function preloadIcons() {
	for (const icon of icons) {
		await loadIcon(icon);
		const i = getIcon(icon);
		if (i) {
			const { body, width, height, left, top } = i;
			const viewBox = `${left} ${top} ${width} ${height}`;
			iconCache[icon] = `
				<svg xmlns="http://www.w3.org/2000/svg" class="w-4 h-4 inline" viewBox="${viewBox}" width="${width}" height="${height}">
					${body}
				</svg>
			`.trim();
		}
	}
}

export function getFileIcon(filename: string) {
	const ext = filename.split('.').pop()?.toLowerCase() || '';
	switch (ext) {
		case 'jpg':
		case 'jpeg':
		case 'png':
		case 'gif':
		case 'bmp':
		case 'svg':
			return ImageIcon;
		case 'mp4':
		case 'avi':
		case 'mkv':
		case 'mov':
		case 'wmv':
			return Video;
		case 'mp3':
		case 'wav':
		case 'flac':
		case 'ogg':
			return Music;
		case 'zip':
		case 'tar':
		case 'gz':
		case 'rar':
		case '7z':
			return Archive;
		case 'exe':
		case 'sh':
		case 'bin':
			return FileText;
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
			return FileText;
	}
}
