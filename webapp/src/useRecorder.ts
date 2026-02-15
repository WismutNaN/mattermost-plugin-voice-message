import {useState, useRef, useCallback, useEffect} from 'react';
import {bestMimeType} from './api';

export type RecState = 'idle' | 'recording' | 'recorded' | 'error';
export interface AudioDevice { deviceId: string; label: string }

export function useRecorder(maxSeconds: number) {
    const [state, setState] = useState<RecState>('idle');
    const [duration, setDuration] = useState(0);
    const [blob, setBlob] = useState<Blob | null>(null);
    const [url, setUrl] = useState('');
    const [devices, setDevices] = useState<AudioDevice[]>([]);
    const [deviceId, setDeviceId] = useState('');
    const [error, setError] = useState('');
    const [levels, setLevels] = useState<number[]>([]);

    const recRef = useRef<MediaRecorder | null>(null);
    const streamRef = useRef<MediaStream | null>(null);
    const chunks = useRef<Blob[]>([]);
    const timer = useRef<any>(null);
    const t0 = useRef(0);
    const analyserRef = useRef<AnalyserNode | null>(null);
    const rafRef = useRef(0);
    const actxRef = useRef<AudioContext | null>(null);

    const cleanup = useCallback(() => {
        streamRef.current?.getTracks().forEach(t => t.stop());
        streamRef.current = null;
        if (timer.current) { clearInterval(timer.current); timer.current = null; }
        cancelAnimationFrame(rafRef.current);
        analyserRef.current = null;
        if (actxRef.current) { try { actxRef.current.close(); } catch {} actxRef.current = null; }
    }, []);

    useEffect(() => () => { cleanup(); if (url) URL.revokeObjectURL(url); }, []);

    const loadDevices = useCallback(async () => {
        try {
            const tmp = await navigator.mediaDevices.getUserMedia({audio: true});
            tmp.getTracks().forEach(t => t.stop());
            const all = await navigator.mediaDevices.enumerateDevices();
            const inputs = all
                .filter(d => d.kind === 'audioinput')
                .map((d, i) => ({deviceId: d.deviceId, label: d.label || `Microphone ${i + 1}`}));
            setDevices(inputs);
            if (!deviceId && inputs.length) {
                setDeviceId(inputs.find(d => d.deviceId === 'default')?.deviceId || inputs[0].deviceId);
            }
        } catch (e: any) {
            setError(micError(e));
            setState('error');
        }
    }, [deviceId]);

    const updateLevels = useCallback(() => {
        const a = analyserRef.current;
        if (!a) return;
        const data = new Uint8Array(a.frequencyBinCount);
        a.getByteFrequencyData(data);
        const bars = 32;
        const step = Math.floor(data.length / bars);
        const result: number[] = [];
        for (let i = 0; i < bars; i++) {
            let sum = 0;
            for (let j = 0; j < step; j++) sum += data[i * step + j];
            result.push(sum / step / 255);
        }
        setLevels(result);
        rafRef.current = requestAnimationFrame(updateLevels);
    }, []);

    const start = useCallback(async () => {
        setError('');
        chunks.current = [];
        setLevels([]);

        const mime = bestMimeType();
        if (!mime) { setError('Browser does not support audio recording.'); setState('error'); return; }

        try {
            const audio: MediaTrackConstraints = deviceId
                ? {deviceId: {exact: deviceId}, echoCancellation: true, noiseSuppression: true}
                : {echoCancellation: true, noiseSuppression: true};

            const stream = await navigator.mediaDevices.getUserMedia({audio});
            streamRef.current = stream;

            // Audio analysis for level bars
            const actx = new (window.AudioContext || (window as any).webkitAudioContext)();
            actxRef.current = actx;
            const src = actx.createMediaStreamSource(stream);
            const analyser = actx.createAnalyser();
            analyser.fftSize = 256;
            src.connect(analyser);
            analyserRef.current = analyser;

            const rec = new MediaRecorder(stream, {mimeType: mime});
            recRef.current = rec;

            rec.ondataavailable = e => { if (e.data.size > 0) chunks.current.push(e.data); };
            rec.onstop = () => {
                const b = new Blob(chunks.current, {type: mime});
                if (url) URL.revokeObjectURL(url);
                const u = URL.createObjectURL(b);
                setBlob(b); setUrl(u); setState('recorded'); setLevels([]);
                cleanup();
            };
            rec.onerror = () => { setError('Recording error.'); setState('error'); cleanup(); };

            rec.start(250);
            t0.current = Date.now();
            setDuration(0);
            setState('recording');

            timer.current = setInterval(() => {
                const elapsed = (Date.now() - t0.current) / 1000;
                setDuration(elapsed);
                if (elapsed >= maxSeconds) rec.stop();
            }, 100);

            rafRef.current = requestAnimationFrame(updateLevels);
        } catch (e: any) {
            setError(micError(e));
            setState('error');
        }
    }, [deviceId, maxSeconds, url, cleanup, updateLevels]);

    const stop = useCallback(() => {
        if (recRef.current?.state === 'recording') recRef.current.stop();
        if (timer.current) { clearInterval(timer.current); timer.current = null; }
        cancelAnimationFrame(rafRef.current);
    }, []);

    const discard = useCallback(() => {
        cleanup();
        if (url) URL.revokeObjectURL(url);
        setBlob(null); setUrl(''); setDuration(0); setLevels([]); setState('idle'); chunks.current = [];
    }, [url, cleanup]);

    return {state, duration, blob, url, devices, deviceId, error, levels, start, stop, discard, setDeviceId, loadDevices};
}

function micError(e: any): string {
    const n = e?.name || '';
    if (n === 'NotAllowedError') return 'Microphone access denied. Allow it in browser settings.';
    if (n === 'NotFoundError') return 'No microphone found. Connect a recording device.';
    if (n === 'NotReadableError') return 'Microphone is busy (used by another app).';
    return `Microphone error: ${e?.message || n}`;
}
