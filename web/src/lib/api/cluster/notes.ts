import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { NoteSchema, type Note } from '$lib/types/info/notes';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getNotes(): Promise<Note[]> {
    return await apiRequest('/cluster/notes', z.array(NoteSchema), 'GET');
}

export async function createNote(title: string, content: string): Promise<APIResponse> {
    return await apiRequest('/cluster/notes', APIResponseSchema, 'POST', {
        title,
        content
    });
}

export async function editNote(id: number, title: string, content: string): Promise<APIResponse> {
    return await apiRequest(`/cluster/notes/${id}`, APIResponseSchema, 'PUT', {
        title,
        content
    });
}

export async function deleteNote(id: number): Promise<APIResponse> {
    return await apiRequest(`/cluster/notes/${id}`, APIResponseSchema, 'DELETE');
}
