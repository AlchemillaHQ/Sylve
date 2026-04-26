/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { browser } from '$app/environment';
import { goto } from '$app/navigation';
import { storage } from '$lib';
import type { JWTClaims } from '$lib/types/auth';
import type { APIResponse } from '$lib/types/common';
import { kvStorage } from '$lib/types/db';
import { handleAPIError } from '$lib/utils/http';
import { buildLoginOptions, isPasskeySupported, serializeCredential } from '$lib/utils/passkeys';
import { sha256 } from '$lib/utils/string';
import { toast } from 'svelte-sonner';

async function parseJSONResponse(response: Response): Promise<any> {
    const contentType = response.headers.get('content-type') || '';
    if (!contentType.includes('application/json') && !contentType.includes('+json')) {
        return null;
    }

    try {
        return await response.json();
    } catch (_e: unknown) {
        return null;
    }
}

function applySuccessfulLogin(payload: any): boolean {
    if (!payload?.hostname || !payload?.token) {
        return false;
    }

    storage.localHostname = payload.hostname;
    storage.hostname = payload.hostname;
    storage.nodeId = payload.nodeId || '';
    storage.token = payload.token || '';
    storage.clusterToken = payload.clusterToken || '';

    return true;
}

async function clearCachedAPIData() {
    try {
        await kvStorage.clear();
    } catch (error) {
        console.warn('Failed to clear cached API data', error);
    }
}

export async function login(
    username: string,
    password: string,
    authType: string,
    remember: boolean
): Promise<boolean> {
    try {
        if (username === '' || password === '') {
            toast.error('Credentials are required', {
                position: 'bottom-center'
            });

            return false;
        }

        if (authType === '') {
            toast.error('Authentication type is required', {
                position: 'bottom-center'
            });

            return false;
        }

        const response = await fetch('/api/auth/login', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify({
                username,
                password,
                authType,
                remember
            })
        });

        const responseData = await parseJSONResponse(response);

        if (response.status === 200 && responseData) {
            if (applySuccessfulLogin(responseData.data)) {
                await clearCachedAPIData();
                return true;
            } else {
                toast.error('Invalid response received', {
                    position: 'bottom-center'
                });
            }

            return false;
        }

        const data = (responseData || {}) as APIResponse;
        handleAPIError(data);

        if (data.error) {
            if (data.error.includes('only_admin_allowed')) {
                toast.error('Only admin users can log in', {
                    position: 'bottom-center'
                });
            } else {
                toast.error('Authentication failed', {
                    position: 'bottom-center'
                });
            }
        } else {
            toast.error('Authentication failed', {
                position: 'bottom-center'
            });
        }

        return false;
    } catch (error) {
        console.error('Login error:', error);
        toast.error('Fatal error logging in, check logs!', {
            position: 'bottom-center'
        });
        return false;
    }

    return false;
}

export async function getLoginConfig(): Promise<{ pamEnabled: boolean }> {
    try {
        const response = await fetch('/api/auth/login/config', {
            method: 'GET'
        });

        const responseData = await parseJSONResponse(response);
        if (
            response.status === 200 &&
            responseData &&
            responseData.data &&
            typeof responseData.data.pamEnabled === 'boolean'
        ) {
            return { pamEnabled: responseData.data.pamEnabled };
        }
    } catch (error) {
        console.warn('Failed to load login config', error);
    }

    return { pamEnabled: true };
}

export async function loginWithPasskey(remember: boolean): Promise<boolean> {
    try {
        if (!isPasskeySupported()) {
            toast.error('Passkeys require HTTPS and browser WebAuthn support', {
                position: 'bottom-center'
            });
            return false;
        }

        const beginResponse = await fetch('/api/auth/passkeys/login/begin', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify({})
        });

        const beginData = await parseJSONResponse(beginResponse);
        if (beginResponse.status !== 200 || !beginData?.data?.requestId || !beginData?.data?.publicKey) {
            const data = (beginData || {}) as APIResponse;
            handleAPIError(data);
            toast.error('Passkey login could not be started', {
                position: 'bottom-center'
            });
            return false;
        }

        const publicKey = buildLoginOptions(beginData.data.publicKey);
        const credential = await navigator.credentials.get({ publicKey });
        if (!credential || !(credential instanceof PublicKeyCredential)) {
            toast.error('Passkey authentication failed', {
                position: 'bottom-center'
            });
            return false;
        }

        const finishResponse = await fetch('/api/auth/passkeys/login/finish', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify({
                requestId: beginData.data.requestId,
                credential: serializeCredential(credential),
                remember
            })
        });

        const finishData = await parseJSONResponse(finishResponse);
        if (finishResponse.status === 200 && finishData?.data && applySuccessfulLogin(finishData.data)) {
            await clearCachedAPIData();
            return true;
        }

        const data = (finishData || {}) as APIResponse;
        handleAPIError(data);
        if (data.error && typeof data.error === 'string' && data.error.includes('only_admin_allowed')) {
            toast.error('Only admin users can log in', {
                position: 'bottom-center'
            });
        } else {
            toast.error('Passkey authentication failed', {
                position: 'bottom-center'
            });
        }
        return false;
    } catch (error) {
        if (error instanceof DOMException && error.name === 'NotAllowedError') {
            toast.error('Passkey request cancelled or timed out', {
                position: 'bottom-center'
            });
            return false;
        }

        console.error('Passkey login error:', error);
        toast.error('Fatal error during passkey login', {
            position: 'bottom-center'
        });
        return false;
    }
}

export function getToken(): string | null {
    if (browser) {
        return storage.token;
    }

    return null;
}

export function getClusterToken(): string | null {
    if (browser) {
        return storage.clusterToken;
    }

    return null;
}

export async function isTokenValid(): Promise<boolean> {
    if (!storage.token) {
        return false;
    }

    try {
        const response = await fetch('/api/health/basic', {
            headers: {
                Authorization: `Bearer ${storage.token}`
            }
        });

        const responseData = await parseJSONResponse(response);

        if (response.status < 400) {
            if (responseData?.hostname) {
                storage.localHostname = responseData.hostname;
                if (!storage.hostname) {
                    storage.hostname = responseData.hostname;
                }
            }
            if (responseData?.nodeId) {
                storage.nodeId = responseData.nodeId;
            }
            return true;
        }
    } catch (_e: unknown) {
        return false;
    }

    return false;
}

export async function isClusterTokenValid(): Promise<boolean> {
    try {
        const clusterToken = storage.clusterToken;
        if (!clusterToken) {
            return true;
        }

        const response = await fetch('/api/health/basic', {
            headers: {
                Authorization: `Bearer ${clusterToken}`,
                'X-Cluster-Token': `Bearer ${clusterToken}`
            }
        });

        const responseData = await parseJSONResponse(response);

        if (response.status < 400) {
            if (responseData?.hostname) {
                storage.localHostname = responseData.hostname;
                if (!storage.hostname) {
                    storage.hostname = responseData.hostname;
                }
            }
            if (responseData?.nodeId) {
                // setLocalStorage('nodeId', response.data.nodeId);
                storage.nodeId = responseData.nodeId;
            }
            return true;
        } else {
            storage.clusterToken = '';
        }
    } catch (_e: unknown) {
        return false;
    }

    return false;
}

export async function logOut(message?: string) {
    const token = storage.token;

    if (token) {
        storage.oldToken = token;
    }

    storage.token = '';
    storage.clusterToken = '';
    storage.localHostname = '';
    storage.hostname = '';
    storage.nodeId = '';
    storage.enabledServices = null;
    storage.enabledServicesByHostname = {};

    if (browser) {
        localStorage.removeItem('token');
        localStorage.removeItem('localHostname');
        localStorage.removeItem('hostname');
        localStorage.removeItem('nodeId');
        localStorage.removeItem('clusterToken');
    }

    await clearCachedAPIData();

    if (message) {
        toast.success(message, {
            position: 'bottom-center'
        });
    }

    goto('/', {
        replaceState: true,
        state: {
            loggedOut: true
        }
    });
}

export async function revokeJWT() {
    try {
        const oldtoken = storage.oldToken;
        if (oldtoken) {
            await fetch('/api/auth/logout', {
                headers: {
                    Authorization: `Bearer ${oldtoken}`
                }
            });

            storage.oldToken = '';
        }
    } catch (_e: unknown) {
        console.error('Failed to revoke JWT');
    }
}

export function getJWTClaims(): JWTClaims | null {
    const token = getToken();
    if (token) {
        try {
            return JSON.parse(atob(token.split('.')[1])) as JWTClaims;
        } catch (e) {
            return null;
        }
    }

    return null;
}

export async function getTokenHash(): Promise<string | null> {
    const token = getToken();
    if (!token) {
        return null;
    }

    return await sha256(token);
}

export async function isInitialized(): Promise<boolean[]> {
    try {
        const response = await fetch('/api/health/basic', {
            headers: {
                Authorization: `Bearer ${storage.token}`
            }
        });

        const responseData = await parseJSONResponse(response);

        if (response.status === 200 && responseData && responseData.data) {
            return [responseData.data.initialized === true, responseData.data.restarted === true];
        }
    } catch (_e: unknown) {
        return [false, false];
    }

    return [false, false];
}
