#!/bin/bash
# Switch Claude Code profile for ghyll development
# Usage: ./switch-profile.sh <profile> [scope]
# Profiles: analyst, architect, adversary, implementer, integrator

set -euo pipefail

PROFILE="${1:?Usage: ./switch-profile.sh <profile> [scope]}"
SCOPE="${2:-}"

PROFILE_FILE=".claude/${PROFILE}.md"

if [ ! -f "$PROFILE_FILE" ]; then
    echo "Error: Profile '$PROFILE' not found at $PROFILE_FILE"
    echo "Available profiles: analyst, architect, adversary, implementer, integrator"
    exit 1
fi

if [ -n "$SCOPE" ]; then
    echo "Activating profile: $PROFILE (scope: $SCOPE)"
    {
        cat "$PROFILE_FILE"
        echo ""
        echo "## Current Scope"
        echo ""
        echo "You are working on: **$SCOPE**"
    } > .claude/CLAUDE.md
else
    echo "Activating profile: $PROFILE"
    cp "$PROFILE_FILE" .claude/CLAUDE.md
fi

echo "Profile written to .claude/CLAUDE.md"
