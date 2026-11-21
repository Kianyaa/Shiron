# Changelog

Shiron shows the newest entry in `/about` and the last few under **Full changelog**.
Add each new version above the others: a level-2 heading with the version, a middle dot and
the date (like the one below), then one bullet line per change.

## v1.0.2 · 2026-11-21
- Fake player counts: when a server reports more players than its player list shows, cards show the real count with a ⚠️ note
- Cards show when the current map started, next to when they were last updated
- More map pictures: simple, dark previews are no longer mistaken for blank ones
- Runs on small free hosts: lighter database library, and the token can come from `DISCORD_TOKEN`

## v1.0.1 · 2026-11-19
- `/notify list`: edit each alert (map, one server or all servers), and Remove all, which also deletes your saved DM/channel choice
- 🔔 Notify me answers privately with a 🔕 Stop alerts button; DM alerts offer Stop alerts directly
- Offline posts show when the server was last seen, back-online posts how long it was down
- Safer at scale: DMs are sent gradually, closed DMs switch to channel mentions, and lost channels pause posts until an admin picks a new one
- Stricter security: `/servers` always needs Manage Server, more blocked address ranges, updated libraries
- New startup screen with live status in the window, and a program icon
- Now licensed under AGPL v3

## v1.0 · 2026-11-19
- Live map cards with map pictures, a player bar and a Join server button
- Map-change posts, offline / back-online alerts, and Refresh that never overwrites history
- 🔔 Notify me: alerts by DM or channel mention, for one server or all of them
- Control panels: `/servers list` for admins and `/notify list` for everyone
- Map pictures from Steam Workshop and your community's Workshop collections
- English, Thai and Simplified Chinese
