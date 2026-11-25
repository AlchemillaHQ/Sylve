import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { NoteSchema, type Note, type Notes } from '$lib/types/info/notes';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';
import { z } from 'zod/v4';

async function notesRequest(
	endpoint: string,
	method: 'GET' | 'POST' | 'PUT' | 'DELETE',
	body?: object,
	options?: NodeAPIRequestOptions
): Promise<Notes | Note | APIResponse> {
	let schema;

	if (method === 'GET') {
		schema = z.array(NoteSchema);
	} else if (method === 'POST') {
		schema = NoteSchema;
	} else {
		schema = APIResponseSchema;
	}

	return await apiRequest(endpoint, schema, method, body, options);
}

export const getNotes = async (options?: NodeAPIRequestOptions): Promise<Notes | APIResponse> => {
	return (await notesRequest('/info/notes', 'GET', undefined, options)) as Notes | APIResponse;
};
export const deleteNote = (id: number, options?: NodeAPIRequestOptions) =>
	notesRequest(`/info/notes/${id}`, 'DELETE', undefined, options);

export const createNote = async (
	title: string,
	content: string,
	options?: NodeAPIRequestOptions
): Promise<Note | APIResponse> => {
	return (await notesRequest('/info/notes', 'POST', { title, content }, options)) as
		| Note
		| APIResponse;
};

export const updateNote = async (
	id: number,
	title: string,
	content: string,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> => {
	return (await notesRequest(
		`/info/notes/${id}`,
		'PUT',
		{ title, content },
		options
	)) as APIResponse;
};

export const deleteNotes = async (
	ids: number[],
	options?: NodeAPIRequestOptions
): Promise<APIResponse> => {
	const query = new URLSearchParams();
	for (const id of ids) query.append('ids', String(id));

	return (await notesRequest(
		`/info/notes?${query.toString()}`,
		'DELETE',
		undefined,
		options
	)) as APIResponse;
};
