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

export function markdownToTailwindHTML(markdown: string): string {
    const html = markdown
        // Headings
        .replace(/^### (.*$)/gim, '<h3 class="text-lg font-semibold my-2">$1</h3>')
        .replace(/^## (.*$)/gim, '<h2 class="text-xl font-bold my-3">$1</h2>')
        .replace(/^# (.*$)/gim, '<h1 class="text-2xl font-bold my-4">$1</h1>')

        // Bold
        .replace(/\*\*(.*?)\*\*/gim, '<strong class="font-bold">$1</strong>')

        // Italic
        .replace(/\*(.*?)\*/gim, '<em class="italic">$1</em>')

        // Links
        .replace(
            /\[([^\]]+)]\(([^)]+)\)/gim,
            '<a href="$2" class="text-blue-600 hover:underline">$1</a>'
        )

        // Paragraphs
        .replace(
            /^\s*(?!<(h[1-3]|ul|ol|li|p|blockquote|pre|code|img|a|strong|em)).+$/gim,
            '<p class="my-2">$&</p>'
        )

        // Line breaks
        .replace(/\n/g, '');

    return html.trim();
}
