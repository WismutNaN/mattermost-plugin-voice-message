import React, {useEffect, useState, useCallback} from 'react';
import {useRecorder} from './useRecorder';
import {uploadVoice, fetchConfig} from './api';

interface Props {
    channelId: string;
    rootId?: string;
    onClose: () => void;
    onSent: () => void;
}

const fmt = (s: number) => `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, '0')}`;

/* SVG Icons */
const MicIcon = () => (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"/>
        <path d="M19 10v2a7 7 0 0 1-14 0v-2"/><line x1="12" y1="19" x2="12" y2="22"/>
    </svg>
);
const StopIcon = () => <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="6" width="12" height="12" rx="2"/></svg>;
const SendIcon = () => (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/>
    </svg>
);
const TrashIcon = () => (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
    </svg>
);
const XIcon = () => (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
        <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
    </svg>
);

const RecorderPanel: React.FC<Props> = ({channelId, rootId, onClose, onSent}) => {
    const [maxDur, setMaxDur] = useState(300);
    const [sending, setSending] = useState(false);
    const rec = useRecorder(maxDur);

    useEffect(() => {
        fetchConfig().then(c => setMaxDur(c.maxDurationSeconds || 300)).catch(() => {});
        rec.loadDevices();
    }, []);

    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                if (rec.state === 'recording') rec.stop();
                else { rec.discard(); onClose(); }
            }
        };
        document.addEventListener('keydown', onKey);
        return () => document.removeEventListener('keydown', onKey);
    }, [rec.state]);

    const handleSend = useCallback(async () => {
        if (!rec.blob) return;
        setSending(true);
        try {
            await uploadVoice(rec.blob, channelId, rec.duration, rootId);
            rec.discard();
            onSent();
        } catch (e: any) {
            alert('Send failed: ' + (e.message || ''));
        } finally {
            setSending(false);
        }
    }, [rec.blob, rec.duration, channelId, rootId]);

    const onOverlay = (e: React.MouseEvent) => {
        if (e.target === e.currentTarget && rec.state === 'idle') { rec.discard(); onClose(); }
    };

    const remaining = Math.max(0, maxDur - rec.duration);
    const showWarning = rec.state === 'recording' && remaining < 30;

    return (
        <div className="vm-overlay" onClick={onOverlay}>
            <div className="vm-panel">
                {/* Header */}
                <div className="vm-header">
                    <div className="vm-header-left">
                        <div className="vm-header-icon"><MicIcon/></div>
                        <span className="vm-title">Voice Message</span>
                    </div>
                    <button className="vm-close" onClick={() => { rec.discard(); onClose(); }} aria-label="Close"><XIcon/></button>
                </div>

                {/* Error banner */}
                {rec.error && <div className="vm-error">{rec.error}</div>}

                {/* Device selector */}
                {rec.devices.length > 1 && rec.state === 'idle' && (
                    <div className="vm-device-wrap">
                        <select className="vm-device" value={rec.deviceId} onChange={e => rec.setDeviceId(e.target.value)}>
                            {rec.devices.map(d => <option key={d.deviceId} value={d.deviceId}>{d.label}</option>)}
                        </select>
                    </div>
                )}

                <div className="vm-body">
                    {/* Timer */}
                    <div className="vm-timer-section">
                        <div className={`vm-timer ${rec.state === 'recording' ? 'vm-timer--rec' : ''} ${showWarning ? 'vm-timer--warn' : ''}`}>
                            {fmt(rec.duration)}
                        </div>
                        {(rec.state === 'idle' || rec.state === 'recording') && (
                            <div className="vm-timer-sub">
                                {rec.state === 'recording' ? `${fmt(remaining)} remaining` : `Max ${fmt(maxDur)}`}
                            </div>
                        )}
                    </div>

                    {/* Live audio levels */}
                    {rec.state === 'recording' && rec.levels.length > 0 && (
                        <div className="vm-levels">
                            {rec.levels.map((v, i) => (
                                <div key={i} className="vm-level-bar" style={{height: `${Math.max(3, v * 100)}%`, opacity: v > 0.05 ? 1 : 0.3}}/>
                            ))}
                        </div>
                    )}

                    {/* Recording indicator */}
                    {rec.state === 'recording' && (
                        <div className="vm-rec-indicator"><div className="vm-rec-dot"/>Recording</div>
                    )}

                    {/* Preview player */}
                    {rec.state === 'recorded' && rec.url && (
                        <div className="vm-preview">
                            <audio controls src={rec.url}/>
                            <div className="vm-preview-hint">Listen before sending</div>
                        </div>
                    )}

                    {/* Sending state */}
                    {sending && (
                        <div className="vm-sending">
                            <div className="vm-spinner"/>
                            <span>Sending…</span>
                        </div>
                    )}

                    {/* Actions */}
                    {!sending && (
                        <div className="vm-actions">
                            {rec.state === 'idle' && (
                                <button className="vm-btn vm-btn--rec" onClick={rec.start} title="Start recording">
                                    <MicIcon/>
                                </button>
                            )}
                            {rec.state === 'recording' && (
                                <button className="vm-btn vm-btn--stop" onClick={rec.stop} title="Stop recording">
                                    <StopIcon/>
                                </button>
                            )}
                            {rec.state === 'recorded' && <>
                                <button className="vm-btn vm-btn--trash" onClick={rec.discard} title="Discard">
                                    <TrashIcon/>
                                </button>
                                <button className="vm-btn vm-btn--send" onClick={handleSend} title="Send">
                                    <SendIcon/>
                                </button>
                            </>}
                            {rec.state === 'error' && (
                                <button className="vm-btn vm-btn--rec" onClick={() => { rec.discard(); rec.loadDevices(); }} title="Try again">
                                    <MicIcon/>
                                </button>
                            )}
                        </div>
                    )}
                </div>

                {/* Footer hint */}
                <div className="vm-footer-hint">
                    {rec.state === 'idle' && 'Click the mic to start • Esc to close'}
                    {rec.state === 'recording' && 'Press Stop or Esc to finish'}
                    {rec.state === 'recorded' && 'Preview your message, then send or discard'}
                </div>
            </div>
        </div>
    );
};

export default RecorderPanel;
