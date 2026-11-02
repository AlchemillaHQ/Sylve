import type { Column, Row } from '$lib/types/components/tree-table';
import type { Note } from '$lib/types/info/notes';
import { escapeHTML } from '$lib/utils/string';

const SAFE_PROTOCOLS = /^(https?:|mailto:)/i;

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

export function markdownToTailwindHTML(markdown: string): string {
	if (!markdown) return '';

	// Escape first so raw HTML can't become tags.
	return (
		escapeHTML(markdown)
			// Headings
			.replace(/^### (.*)$/gim, '<h3 class="text-lg font-semibold my-2">$1</h3>')
			.replace(/^## (.*)$/gim, '<h2 class="text-xl font-bold my-3">$1</h2>')
			.replace(/^# (.*)$/gim, '<h1 class="text-2xl font-bold my-4">$1</h1>')

			// Bold then italic
			.replace(/\*\*(.*?)\*\*/gim, '<strong class="font-semibold">$1</strong>')
			.replace(/\*(.*?)\*/gim, '<em class="italic">$1</em>')

			// Links: validate URL to reject javascript:/data: URIs
			.replace(/\[([^\]]+)]\(([^)]+)\)/gim, (_match, text: string, rawUrl: string) => {
				const url = rawUrl.trim();
				const safeUrl = SAFE_PROTOCOLS.test(url) ? url : '#';
				return `<a href="${safeUrl}" rel="noopener noreferrer" target="_blank" class="text-blue-600 hover:underline">${text}</a>`;
			})

			// Paragraphs: wrap any line not starting with a block-level tag
			.replace(
				/^\s*(?!<(h[1-3]|ul|ol|li|p|blockquote|pre|code|img|a|strong|em)).+$/gim,
				'<p class="my-2">$&</p>'
			)

			// Collapse leftover newlines
			.replace(/\n+/g, '')
			.trim()
	);
}
