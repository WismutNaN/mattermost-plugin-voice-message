#!/bin/bash
# Alternative plugin installation - bypasses upload API
# Usage: ./install.sh /path/to/mattermost
#
# This extracts the built plugin directly into the Mattermost plugins directory.
# Use this if the System Console upload doesn't work.

set -e

MM_DIR="${1:-/opt/mattermost}"
PLUGIN_ID="com.scientia.voice-message"
# Pick the newest built bundle (by version)
BUNDLE=$(ls -1 "dist/${PLUGIN_ID}-"*.tar.gz 2>/dev/null | sort -V | tail -n 1)

if [ ! -f "$BUNDLE" ]; then
    echo "ERROR: $BUNDLE not found. Run 'make dist' first."
    exit 1
fi

if [ ! -d "$MM_DIR" ]; then
    echo "ERROR: Mattermost directory not found: $MM_DIR"
    echo "Usage: $0 /path/to/mattermost"
    exit 1
fi

PLUGINS_DIR="$MM_DIR/plugins"
CLIENT_DIR="$MM_DIR/client/plugins"

echo "==> Installing to $PLUGINS_DIR ..."

# Remove old version
rm -rf "$PLUGINS_DIR/$PLUGIN_ID"

# Extract plugin
cd dist
tar -xzf "$(basename $BUNDLE)" -C "$PLUGINS_DIR"
cd ..

# Verify structure
echo "==> Verifying..."
if [ ! -f "$PLUGINS_DIR/$PLUGIN_ID/plugin.json" ]; then
    echo "ERROR: plugin.json not found after extraction!"
    ls -la "$PLUGINS_DIR/$PLUGIN_ID/" 2>/dev/null || echo "  Directory doesn't exist"
    exit 1
fi

if [ ! -f "$PLUGINS_DIR/$PLUGIN_ID/webapp/dist/main.js" ]; then
    echo "ERROR: webapp/dist/main.js not found!"
    echo "  Contents:"
    find "$PLUGINS_DIR/$PLUGIN_ID" -type f | head -20
    exit 1
fi

echo "  ✓ plugin.json"
echo "  ✓ webapp/dist/main.js"
echo "  ✓ server binaries:"
ls "$PLUGINS_DIR/$PLUGIN_ID/server/dist/" 2>/dev/null | sed 's/^/    /'

# Fix ownership (match mattermost user if exists)
MM_USER=$(stat -c '%U' "$MM_DIR/bin/mattermost" 2>/dev/null || echo "")
if [ -n "$MM_USER" ] && [ "$MM_USER" != "root" ]; then
    echo "==> Setting ownership to $MM_USER..."
    chown -R "$MM_USER:$MM_USER" "$PLUGINS_DIR/$PLUGIN_ID"
fi

# Clean client cache
rm -rf "$CLIENT_DIR/$PLUGIN_ID" 2>/dev/null

echo ""
echo "✅  Plugin installed to $PLUGINS_DIR/$PLUGIN_ID"
echo ""
echo "Next steps:"
echo "  1. Restart Mattermost: systemctl restart mattermost"
echo "  2. Enable plugin in System Console → Plugins → Plugin Management"
echo ""
echo "If activation still fails, check:"
echo "  ls -la $PLUGINS_DIR/$PLUGIN_ID/webapp/dist/"
echo "  ls -la $CLIENT_DIR/"
