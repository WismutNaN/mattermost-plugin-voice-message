// Webapp -> plugin server API helper.
const PLUGIN_ID = 'com.scientia.voice-message';

export type VoiceConfig = {
    maxDurationSeconds: number;
    enableTranscription: boolean;
    autoTranscribe: boolean;
    transcriptionMaxDuration: number;
};

type ReduxStoreLike = { getState: () => any };

function getStore(): ReduxStoreLike | null {
    const s = (window as any).__vmStore;
    return s && typeof s.getState === 'function' ? s : null;
}

function parseCookie(name: string): string | null {
    try {
        for (const p of document.cookie.split(';').map(x => x.trim())) {
            if (p.startsWith(name + '=')) return decodeURIComponent(p.slice(name.length + 1));
        }
    } catch { /* ignore */ }
    return null;
}

function normalizeBasePath(p: string): string {
    if (!p) return '';
    if (!p.startsWith('/')) p = '/' + p;
    p = p.replace(/\/+$/, '');
    return (p === '/' || p === '') ? '' : p;
}

function getBasePath(): string {
    const b = (window as any).basename as string | undefined;
    if (b) return normalizeBasePath(b);
    try {
        const pn = window.location?.pathname || '';
        const idx = pn.indexOf('/channels/');
        if (idx >= 0) return normalizeBasePath(pn.slice(0, idx));
        const pidx = pn.indexOf('/plugins/');
        if (pidx >= 0) return normalizeBasePath(pn.slice(0, pidx));
    } catch { /* ignore */ }
    const store = getStore();
    const siteURL: string | undefined = store?.getState?.()?.entities?.general?.config?.SiteURL;
    if (siteURL) { try { return normalizeBasePath(new URL(siteURL).pathname || ''); } catch {} }
    return '';
}

function pluginBaseURL(): string {
    return `${getBasePath()}/plugins/${PLUGIN_ID}`;
}

function getAuthHeaders(extra?: Record<string, string>): Headers {
    const headers = new Headers(extra || {});
    headers.set('X-Requested-With', 'XMLHttpRequest');

    const state = getStore()?.getState?.();
    const creds = state?.entities?.general?.credentials || {};

    const csrf: string | undefined =
        creds?.csrfToken || creds?.csrf || creds?.csrf_token || parseCookie('MMCSRF') || undefined;
    if (csrf) headers.set('X-CSRF-Token', csrf);

    const token: string | undefined =
        creds?.token || creds?.authToken || creds?.accessToken ||
        (() => { try { return window.localStorage?.getItem('token') || undefined; } catch { return undefined; } })();
    if (token) headers.set('Authorization', `Bearer ${token}`);

    headers.set('Accept', 'application/json');
    return headers;
}

async function fetchJSON<T>(url: string, init: RequestInit): Promise<T> {
    const res = await fetch(url, { ...init, credentials: 'include' });
    if (!res.ok) {
        let msg = `${res.status} ${res.statusText}`;
        try {
            const body = await res.text();
            // Try JSON first (transcription errors return {error, detail}).
            try {
                const j = JSON.parse(body);
                if (j.error) msg = j.detail ? `${j.error} (${j.detail})` : j.error;
            } catch {
                if (body) msg = `${res.status}: ${body}`;
            }
        } catch { /* ignore */ }
        throw new Error(msg);
    }
    return (await res.json()) as T;
}

export async function fetchConfig(): Promise<VoiceConfig> {
    return fetchJSON<VoiceConfig>(`${pluginBaseURL()}/api/v1/config`, {
        method: 'GET',
        headers: getAuthHeaders(),
    });
}

export async function uploadVoice(
    blob: Blob, channelId: string, durationSeconds: number, rootId?: string,
): Promise<{post_id: string; file_id: string}> {
    const params = new URLSearchParams();
    params.set('channel_id', channelId);
    if (rootId) params.set('root_id', rootId);
    params.set('duration', String(Math.max(0, Math.floor(durationSeconds))));

    return fetchJSON<{post_id: string; file_id: string}>(
        `${pluginBaseURL()}/api/v1/upload?${params.toString()}`,
        { method: 'POST', headers: getAuthHeaders({'Content-Type': blob.type || 'application/octet-stream'}), body: blob },
    );
}

export async function transcribeVoice(postId: string): Promise<{transcript: string; cached: boolean}> {
    return fetchJSON<{transcript: string; cached: boolean}>(
        `${pluginBaseURL()}/api/v1/transcribe?post_id=${encodeURIComponent(postId)}`,
        { method: 'POST', headers: getAuthHeaders() },
    );
}

export function bestMimeType(): string {
    const candidates = [
        'audio/webm;codecs=opus', 'audio/ogg;codecs=opus',
        'audio/webm', 'audio/ogg', 'audio/mp4',
    ];
    try {
        if (!(window as any).MediaRecorder) return '';
        for (const c of candidates) {
            try { if ((window as any).MediaRecorder.isTypeSupported?.(c)) return c; } catch {}
        }
    } catch {}
    return '';
}
