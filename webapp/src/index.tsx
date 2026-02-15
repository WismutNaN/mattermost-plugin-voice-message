import React, {useState, useEffect, useCallback} from 'react';
import RecorderPanel from './RecorderPanel';
import VoicePost from './VoicePost';
import './styles.css';

const PLUGIN_ID = 'com.scientia.voice-message';

/* Mic icon for buttons */
const MicIcon16 = () => (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor"
         strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
        <path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"/>
        <path d="M19 10v2a7 7 0 0 1-14 0v-2"/>
        <line x1="12" y1="19" x2="12" y2="22"/>
    </svg>
);

/* Root Component: manages recorder overlay */
const VoiceRoot: React.FC = () => {
    const [open, setOpen] = useState(false);
    const [channelId, setChannelId] = useState('');
    const [rootId, setRootId] = useState<string | undefined>();

    useEffect(() => {
        (window as any).__vmOpen = (chId: string, rId?: string) => {
            setChannelId(chId);
            setRootId(rId);
            setOpen(true);
        };
        return () => { delete (window as any).__vmOpen; };
    }, []);

    const close = useCallback(() => setOpen(false), []);

    if (!open || !channelId) return null;
    return <RecorderPanel channelId={channelId} rootId={rootId} onClose={close} onSent={close}/>;
};

/* Helper to get current channel ID from Redux store */
function getCurrentChannelId(store: any): string {
    try { return store.getState()?.entities?.channels?.currentChannelId || ''; }
    catch { return ''; }
}

/* Helper to get current root/thread ID */
function getCurrentRootId(store: any): string | undefined {
    try {
        const state = store.getState();
        // If user has a thread open in the RHS
        const selectedPost = state?.views?.rhs?.selectedPostId;
        return selectedPost || undefined;
    } catch { return undefined; }
}

/* Plugin Class */
class VoiceMessagePlugin {
    initialize(registry: any, store: any) {
        (window as any).__vmStore = store;

        // Root overlay component
        registry.registerRootComponent(VoiceRoot);

        // Channel header button
        registry.registerChannelHeaderButtonAction(
            <MicIcon16/>,
            (channel: any) => {
                const chId = channel?.id || getCurrentChannelId(store);
                if (chId) (window as any).__vmOpen?.(chId);
            },
            'Voice Message',
            'Record a voice message',
        );

        // File upload method — mic button in the "+" attach menu
        registry.registerFileUploadMethod(
            <MicIcon16/>,
            () => {
                const chId = getCurrentChannelId(store);
                if (chId) (window as any).__vmOpen?.(chId, getCurrentRootId(store));
            },
            'Voice Message',
        );

        // Custom post type renderer
        registry.registerPostTypeComponent('custom_voice_message', VoicePost);

        // Intercept /voice and /vm on web/desktop
        registry.registerSlashCommandWillBePostedHook((message: string, args: any) => {
            const cmd = message.trim();
            if (cmd === '/voice' || cmd.startsWith('/voice ') || cmd === '/audiomsg' || cmd.startsWith('/audiomsg ')) {
                const chId = args?.channel_id || getCurrentChannelId(store);
                if (chId) {
                    setTimeout(() => (window as any).__vmOpen?.(chId, args?.root_id), 0);
                }
                return {};
            }
            return {message, args};
        });
    }

    uninitialize() {
        delete (window as any).__vmOpen;
        delete (window as any).__vmStore;
    }
}

/* Register plugin */
(window as any).registerPlugin(PLUGIN_ID, new VoiceMessagePlugin());
