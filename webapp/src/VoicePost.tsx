import React, {useState, useRef, useEffect, useCallback, useMemo} from 'react';
import {transcribeVoice, fetchConfig, VoiceConfig} from './api';

const SPEEDS = [1, 1.25, 1.5, 2];
const BAR_COUNT = 40;
const fmt = (s: number) => {
    const m = Math.floor(s / 60);
    const sec = Math.floor(s % 60);
    return `${m}:${String(sec).padStart(2, '0')}`;
};

function genBars(seed: string): number[] {
    let h = 0;
    for (let i = 0; i < seed.length; i++) h = ((h << 5) - h) + seed.charCodeAt(i) | 0;
    const bars: number[] = [];
    for (let i = 0; i < BAR_COUNT; i++) {
        h = ((h << 5) - h) + i | 0;
        // Generate more organic-looking waveform
        const base = 0.15 + Math.abs(h % 70) / 100;
        const wave = Math.sin(i * 0.3 + (h % 10)) * 0.15;
        bars.push(Math.min(1, Math.max(0.1, base + wave)));
    }
    return bars;
}

/* SVG Icons */
const PlayIcon = () => (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor"><polygon points="6 3 20 12 6 21"/></svg>
);
const PauseIcon = () => (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
        <rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/>
    </svg>
);
const TranscriptIcon = () => (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
        <polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/>
        <line x1="16" y1="17" x2="8" y2="17"/><polyline points="10 9 9 9 8 9"/>
    </svg>
);
const ChevronDown = () => (
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
        <polyline points="6 9 12 15 18 9"/>
    </svg>
);
const ChevronUp = () => (
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
        <polyline points="18 15 12 9 6 15"/>
    </svg>
);

const VoicePost: React.FC<{post: any; theme?: any}> = ({post}) => {
    const [playing, setPlaying] = useState(false);
    const [curTime, setCurTime] = useState(0);
    const [totalDur, setTotalDur] = useState(0);
    const [spdIdx, setSpdIdx] = useState(0);
    const [transcript, setTranscript] = useState<string | null>(null);
    const [transcribing, setTranscribing] = useState(false);
    const [transcriptError, setTranscriptError] = useState<string | null>(null);
    const [showTranscript, setShowTranscript] = useState(false);
    const [config, setConfig] = useState<VoiceConfig | null>(null);
    const audioRef = useRef<HTMLAudioElement | null>(null);
    const rafRef = useRef(0);
    const blobUrl = useRef('');

    const fileDur = parseFloat(post.props?.voice_duration || '0');
    const fileIds: string[] = post.file_ids || [];
    const base = (window as any).basename || '';
    const fileURL = fileIds.length > 0 ? `${base}/api/v4/files/${fileIds[0]}` : '';
    const bars = useMemo(() => genBars(post.id || ''), [post.id]);

    // Read existing transcript from post props
    const existingTranscript = post.props?.voice_transcript || null;

    useEffect(() => {
        if (existingTranscript) setTranscript(existingTranscript);
    }, [existingTranscript]);

    useEffect(() => {
        fetchConfig().then(c => setConfig(c)).catch(() => {});
    }, []);

    useEffect(() => {
        if (!fileURL) return;
        const a = new Audio();
        a.preload = 'metadata';
        a.onloadedmetadata = () => { if (isFinite(a.duration)) setTotalDur(a.duration); };
        a.onended = () => { setPlaying(false); setCurTime(0); a.currentTime = 0; };
        audioRef.current = a;
        return () => { a.pause(); a.src = ''; if (blobUrl.current) URL.revokeObjectURL(blobUrl.current); };
    }, [fileURL]);

    const tick = useCallback(() => {
        if (audioRef.current) setCurTime(audioRef.current.currentTime);
        if (playing) rafRef.current = requestAnimationFrame(tick);
    }, [playing]);

    useEffect(() => {
        if (playing) rafRef.current = requestAnimationFrame(tick);
        return () => cancelAnimationFrame(rafRef.current);
    }, [playing, tick]);

    const togglePlay = useCallback(() => {
        const a = audioRef.current;
        if (!a) return;
        if (playing) { a.pause(); setPlaying(false); return; }

        const play = () => {
            a.playbackRate = SPEEDS[spdIdx];
            a.play().then(() => setPlaying(true)).catch(() => {});
        };

        if (!blobUrl.current) {
            fetch(fileURL, {credentials: 'include'})
                .then(r => r.blob())
                .then(b => {
                    blobUrl.current = URL.createObjectURL(b);
                    a.src = blobUrl.current;
                    a.onloadedmetadata = () => { if (isFinite(a.duration)) setTotalDur(a.duration); };
                    play();
                })
                .catch(() => {});
        } else {
            play();
        }
    }, [playing, fileURL, spdIdx]);

    const seek = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
        const a = audioRef.current;
        const d = totalDur || fileDur;
        if (!a || !d) return;
        const rect = e.currentTarget.getBoundingClientRect();
        const pct = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
        a.currentTime = pct * d;
        setCurTime(a.currentTime);
    }, [totalDur, fileDur]);

    const cycleSpeed = useCallback(() => {
        const next = (spdIdx + 1) % SPEEDS.length;
        setSpdIdx(next);
        if (audioRef.current) audioRef.current.playbackRate = SPEEDS[next];
    }, [spdIdx]);

    const handleTranscribe = useCallback(async () => {
        if (transcribing) return;
        setTranscribing(true);
        setTranscriptError(null);
        try {
            const result = await transcribeVoice(post.id);
            setTranscript(result.transcript);
            setShowTranscript(true);
        } catch (e: any) {
            setTranscriptError(e.message || 'Unknown error');
        } finally {
            setTranscribing(false);
        }
    }, [post.id, transcribing]);

    if (!fileURL) {
        return <div className="vp-unavailable">🎤 Voice message (file unavailable)</div>;
    }

    const dur = totalDur || fileDur;
    const progress = dur > 0 ? curTime / dur : 0;
    const playedBars = Math.floor(progress * BAR_COUNT);
    const canTranscribe = config?.enableTranscription && !transcript;

    return (
        <div className="vp-container">
            <div className="vp-player">
                <button className={`vp-play ${playing ? 'vp-play--active' : ''}`} onClick={togglePlay} aria-label={playing ? 'Pause' : 'Play'}>
                    {playing ? <PauseIcon/> : <PlayIcon/>}
                </button>
                <div className="vp-bars" onClick={seek} role="slider" aria-label="Seek">
                    {bars.map((h, i) => (
                        <div
                            key={i}
                            className={`vp-bar ${i < playedBars ? 'vp-bar--played' : ''} ${i === playedBars && playing ? 'vp-bar--current' : ''}`}
                            style={{height: `${Math.max(2, Math.round(h * 22))}px`}}
                        />
                    ))}
                </div>
                <span className="vp-time">{playing || curTime > 0 ? fmt(curTime) : fmt(dur)}</span>
                <button className="vp-speed" onClick={cycleSpeed} title="Playback speed">{SPEEDS[spdIdx]}×</button>
                {canTranscribe && (
                    <button
                        className={`vp-transcribe-btn ${transcribing ? 'vp-transcribe-btn--loading' : ''} ${transcriptError ? 'vp-transcribe-btn--error' : ''}`}
                        onClick={handleTranscribe}
                        title={transcriptError ? `Retry (${transcriptError})` : 'Transcribe'}
                        disabled={transcribing}
                    >
                        {transcribing ? <div className="vp-mini-spinner"/> : <TranscriptIcon/>}
                    </button>
                )}
                {transcript && (
                    <button
                        className="vp-transcript-toggle"
                        onClick={() => setShowTranscript(!showTranscript)}
                        title={showTranscript ? 'Hide transcript' : 'Show transcript'}
                    >
                        <TranscriptIcon/>
                        {showTranscript ? <ChevronUp/> : <ChevronDown/>}
                    </button>
                )}
            </div>
            {transcriptError && !transcript && (
                <div className="vp-error">{transcriptError}</div>
            )}
            {transcript && showTranscript && (
                <div className="vp-transcript">
                    <div className="vp-transcript-text">{transcript}</div>
                </div>
            )}
        </div>
    );
};

export default VoicePost;
