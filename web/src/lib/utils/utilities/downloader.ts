import type { Column, Row } from '$lib/types/components/tree-table';
import type { Download } from '$lib/types/utilities/downloader';
import humanFormat from 'human-format';
import type { CellComponent } from 'tabulator-tables';
import { generateNumberFromString } from '../numbers';
import { renderWithIcon } from '../table';
import { escapeHTML } from '../string';

export function generateTableData(data: Download[]): { rows: Row[]; columns: Column[] } {
	const columns: Column[] = [
		{
			field: 'id',
			title: 'ID',
			visible: false
		},
		{
			field: 'uuid',
			title: 'UUID',
			visible: false
		},
		{
			field: 'name',
			title: 'Name',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				const row = cell.getRow();
				const data = row.getData();
				if (data.type !== '-') {
					if (data.type === 'torrent') {
						return renderWithIcon('mdi:magnet', value);
					} else if (data.type === 'http') {
						return renderWithIcon('mdi:internet', value);
					} else {
						return renderWithIcon('mdi:file', value);
					}
				}

				return renderWithIcon('mdi:file', value);
			}
		},
		{
			field: 'size',
			title: 'Size',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				const error = cell.getRow().getData().error;

				if (error) {
					return '-';
				}

				if (value === 0 || value === '0') {
					return renderWithIcon('eos-icons:three-dots-loading', '');
				}

				return humanFormat(value);
			}
		},
		{
			field: 'type',
			title: 'Type',
			visible: false
		},
		{
			field: 'progress',
			title: 'Progress',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				const error = cell.getRow().getData().error;
				const status = cell.getRow().getData().status;

				if (status === 'processing') {
					return renderWithIcon('eos-icons:three-dots-loading', 'Processing');
				}

				if (error) {
					if (error.includes('failed to verify certificate')) {
						return renderWithIcon(
							'mdi:alert-circle-outline',
							'Error',
							'',
							'Certificate verification failed'
						);
					} else {
						return renderWithIcon('mdi:alert-circle-outline', 'Error', '', escapeHTML(error));
					}
				}

				if (value === '-') {
					return '-';
				}

				if (value >= 0 && value < 100) {
					return renderWithIcon('line-md:downloading-loop', `${value} %`);
				}

				return renderWithIcon('lets-icons:check-fill', '100 %');
			}
		},
		{
			field: 'parentUUID',
			title: 'Parent UUID',
			visible: false
		}
	];

	const rows: Row[] = [];

	for (const download of data) {
		const row: Row = {
			id: download.id,
			uuid: download.uuid,
			name: download.name,
			size: download.size,
			type: download.type,
			progress: download.progress,
			error: download.error,
			status: download.status,
			children: []
		};

		for (const file of download.files) {
			const childRow: Row = {
				id: generateNumberFromString(file.id + 'file'),
				uuid: '-',
				name: file.name,
				size: file.size,
				type: '-',
				children: [],
				progress: '-',
				parentUUID: download.uuid,
				status: '-'
			};

			row.children?.push(childRow);
		}

		rows.push(row);
	}

	return {
		rows: rows,
		columns: columns
	};
}

export function getISOs(
	downloads: Download[],
	includeImg: boolean = false
): { label: string; value: string }[] {
	const options: { label: string; value: string }[] = [];

	for (const download of downloads || []) {
		if (download.progress !== 100) continue;

		const addIfMatch = (name: string) => {
			if (
				name.endsWith('.iso') ||
				(includeImg && (name.endsWith('.img') || name.endsWith('.raw')))
			) {
				options.push({ label: name, value: download.uuid });
			}
		};

		if (download.type === 'http') {
			addIfMatch(download.name);
			if (download.extractedPath) {
				if (download.extractedPath.includes('/')) {
					const parts = download.extractedPath.split('/');
					addIfMatch(parts[parts.length - 1]);
				} else {
					addIfMatch(download.extractedPath);
				}
			}
		} else if (download.type === 'torrent' && Array.isArray(download.files)) {
			for (const file of download.files) {
				addIfMatch(file.name);
			}
		}
	}

	return [...new Map(options.map((item) => [item['value'], item])).values()];
}
