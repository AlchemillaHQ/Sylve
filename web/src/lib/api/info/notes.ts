import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { NoteSchema, NotesSchema, type Note, type Notes } from '$lib/types/info/notes';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod';

async function notesRequest(
	endpoint: string,
	method: 'GET' | 'POST' | 'PUT' | 'DELETE',
	body?: object
): Promise<Notes | Note | APIResponse> {
	let schema;

	if (method === 'GET') {
		schema = z.array(NoteSchema);
	} else if (method === 'POST') {
		schema = NoteSchema;
	} else {
		schema = APIResponseSchema;
	}

	return await apiRequest(endpoint, schema, method, body);
}

export const getNotes = () => notesRequest('/info/notes', 'GET');
export const deleteNote = (id: number) => notesRequest(`/info/notes/${id}`, 'DELETE');

export const createNote = async (title: string, content: string): Promise<Note | APIResponse> => {
	return (await notesRequest('/info/notes', 'POST', { title, content })) as Note | APIResponse;
};

export const updateNote = async (
	id: number,
	title: string,
	content: string
): Promise<APIResponse> => {
	return (await notesRequest(`/info/notes/${id}`, 'PUT', { title, content })) as APIResponse;
};

export const deleteNotes = async (ids: number[]): Promise<APIResponse> => {
	return (await notesRequest('/info/notes/bulk-delete', 'POST', { ids })) as APIResponse;
};
