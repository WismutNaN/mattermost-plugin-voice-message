#!/bin/bash
# Diagnostic script for voice-message plugin installation issues
# Usage: ./diagnose.sh /path/to/mattermost

MM_DIR="${1:-/opt/mattermost}"
PLUGIN_ID="com.scientia.voice-message"

echo "=== Voice Message Plugin Diagnostics ==="
echo ""

# 1. Check Mattermost directory
echo "1. Mattermost directory: $MM_DIR"
if [ ! -d "$MM_DIR" ]; then
    echo "   ❌ Not found! Provide correct path: $0 /path/to/mattermost"
    exit 1
fi
echo "   ✓ Exists"

# 2. Check plugins directory
PLUGINS="$MM_DIR/plugins"
echo ""
echo "2. Plugins directory: $PLUGINS"
if [ ! -d "$PLUGINS" ]; then
    echo "   ❌ Not found!"
else
    echo "   ✓ Exists"
    echo "   Owner: $(stat -c '%U:%G' $PLUGINS 2>/dev/null || ls -ld $PLUGINS | awk '{print $3":"$4}')"
    echo "   Permissions: $(stat -c '%a' $PLUGINS 2>/dev/null || ls -ld $PLUGINS | awk '{print $1}')"
fi

# 3. Check client/plugins directory
CLIENT="$MM_DIR/client/plugins"
echo ""
echo "3. Client plugins directory: $CLIENT"
if [ ! -d "$CLIENT" ]; then
    echo "   ❌ Not found! Create it: mkdir -p $CLIENT"
else
    echo "   ✓ Exists"
    echo "   Owner: $(stat -c '%U:%G' $CLIENT 2>/dev/null || ls -ld $CLIENT | awk '{print $3":"$4}')"
    echo "   Permissions: $(stat -c '%a' $CLIENT 2>/dev/null || ls -ld $CLIENT | awk '{print $1}')"
fi

# 4. Check plugin files
PDIR="$PLUGINS/$PLUGIN_ID"
echo ""
echo "4. Plugin directory: $PDIR"
if [ ! -d "$PDIR" ]; then
    echo "   ❌ Plugin not installed (directory not found)"
else
    echo "   ✓ Exists"
    echo ""
    echo "   Files:"
    find "$PDIR" -type f | sed 's|^|   |'
    echo ""
    echo "   Checking critical files:"
    for f in "plugin.json" "webapp/dist/main.js"; do
        if [ -f "$PDIR/$f" ]; then
            echo "   ✓ $f ($(stat -c '%s' $PDIR/$f 2>/dev/null || wc -c < $PDIR/$f) bytes)"
        else
            echo "   ❌ $f MISSING!"
        fi
    done

    # Check server binary for current platform
    ARCH=$(uname -m)
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    [ "$ARCH" = "x86_64" ] && ARCH="amd64"
    [ "$ARCH" = "aarch64" ] && ARCH="arm64"
    BIN="server/dist/plugin-${OS}-${ARCH}"
    if [ -f "$PDIR/$BIN" ]; then
        echo "   ✓ $BIN ($(stat -c '%s' $PDIR/$BIN 2>/dev/null || wc -c < $PDIR/$BIN) bytes)"
    else
        echo "   ❌ $BIN MISSING! (expected for ${OS}-${ARCH})"
        echo "   Available binaries:"
        ls "$PDIR/server/dist/" 2>/dev/null | sed 's/^/      /'
    fi
fi

# 5. Check Mattermost process
echo ""
echo "5. Mattermost process:"
if pgrep -f "mattermost" > /dev/null 2>&1; then
    echo "   ✓ Running (PID: $(pgrep -f 'mattermost server' | head -1 || pgrep -f 'mattermost' | head -1))"
    MM_USER=$(ps -o user= -p $(pgrep -f 'mattermost' | head -1) 2>/dev/null)
    echo "   Running as: $MM_USER"
else
    echo "   ⚠ Not running or not detected"
fi

# 6. Recent logs
echo ""
echo "6. Recent plugin-related logs:"
LOG="$MM_DIR/logs/mattermost.log"
if [ -f "$LOG" ]; then
    grep -i "voice-message\|com.scientia" "$LOG" | tail -10 | sed 's/^/   /'
    if [ $? -ne 0 ]; then
        echo "   No plugin-related entries found"
    fi
else
    echo "   Log file not found at $LOG"
    echo "   Try: journalctl -u mattermost --since '5 min ago' | grep -i plugin"
fi

echo ""
echo "=== Done ==="
