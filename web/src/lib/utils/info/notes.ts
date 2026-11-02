import type { Column, Row } from '$lib/types/components/tree-table';
import type { Note } from '$lib/types/info/notes';

export function generateTableData(
	columns: Column[],
	notes: Note[]
): { rows: Row[]; columns: Column[] } {
	const rows: Row[] = [];

	for (const note of notes) {
		const row: Row = {
			id: note.id,
			name: note.title,
			content: note.content,
			createdAt: note.createdAt,
			updatedAt: note.updatedAt
		};

		rows.push(row);
	}

	return {
		rows: rows,
		columns: columns
	};
}
