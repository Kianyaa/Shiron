<div align="center">

<img src="assets/shiron.png" alt="Shiron" width="140">

# Shiron

**A small, fast Discord bot that tracks Counter-Strike 2 servers.**<br>
Live map cards, map-change alerts and map pictures. One program file, no database server.

[![Stars](https://img.shields.io/github/stars/Kianyaa/Shiron?style=flat&logo=github&color=8787ff)](https://github.com/Kianyaa/Shiron/stargazers)
[![Go](https://img.shields.io/badge/Go-1.27+-00ADD8?logo=go&logoColor=white)](go.mod)
[![License: AGPL v3](https://img.shields.io/badge/license-AGPL%20v3-blue)](LICENSE)
[![Discord](https://img.shields.io/badge/Discord-bot-5865F2?logo=discord&logoColor=white)](#1-create-the-discord-application)
[![Last commit](https://img.shields.io/github/last-commit/Kianyaa/Shiron?color=5fd787)](https://github.com/Kianyaa/Shiron/commits/main)

**English** · [ภาษาไทย](README.th.md) · [简体中文](README.zh-CN.md)

</div>

> [!TIP]
> If Shiron is useful to your community, please give it a ⭐ on GitHub. It helps other CS2 server owners find it.

## Features

- 🗺️ **Map-change posts** in a channel, with a picture of the map and an optional minimum player count.
- 📊 **`/map` and `/players`** show live server info, with a **Join server** button that opens CS2 and connects.
- 🔔 **Alerts** by DM or a channel mention when a map you want comes up. Wildcards like `ze_*` work, for one server or all of them.
- 🟢 **Offline / back-online posts** that say when a server was last seen and how long it was down.
- 🖼️ **Map pictures** from Steam Workshop, community packs or your own Workshop collections.
- 🛠️ **Control panels** with buttons: `/servers list` for admins, `/notify list` for everyone.
- 🌏 **English, Thai and Simplified Chinese**, following each user's Discord language.
- 🪶 **Lean**: one program, SQLite built in, no privileged intents, runs on a free cloud server.

<p align="center"><img src="assets/cli.svg" alt="Shiron's startup window: banner, setup steps, status panel and live status lines" width="720"></p>

## Commands

| Command | Who |
|---|---|
| `/servers add ip [channel] [min_players] [style]` | Manage Server |
| `/servers list` — manage servers (channel, card style, min players, remove), map pictures and Workshop collections | Manage Server |
| `/map [server]`, `/players [server]` | everyone |
| `/notify add map [server]` — leave out `server` to be alerted on all servers | everyone |
| `/notify list` — see and remove your alerts, switch DM / channel | everyone |
| `/about` — version, what's new and stats | everyone |
| `/ping`, `/help`, `/invite` | everyone |

Releasing an update: add a section like the v1.0 one (heading `v1.1 · YYYY-MM-DD`, then bullet points) above it in [CHANGELOG.md](CHANGELOG.md) and rebuild. `/about` and the startup window read the version from it.

Map cards also have buttons: Join server, Copy IP, Players, Refresh and 🔔 Notify me.

Replies use each user's Discord language. Automatic map posts use the Discord server's language.

## 1. Create the Discord application

1. Open https://discord.com/developers/applications and click **New Application**. Name it **Shiron**.
2. **Bot** tab: click **Reset Token** and copy the token. It is the bot's password: never share it or commit it.
3. **Bot** tab: keep **Public Bot** on so anyone can invite it. Leave all **Privileged Gateway Intents** off; Shiron doesn't need them.

The invite link is printed when the bot starts, and `/invite` shows it too.

## 2. Run

Needs Go 1.27+ (only to build).

Put the bot token in a file named `token.txt` next to the program (it is git-ignored), then:

```bash
go build -o shiron .
./shiron
```

On Windows: `go build -o shiron.exe .`, then double-click `shiron.exe`. A window shows when the bot is online; closing it stops the bot. If something is wrong, the window stays open and says what.

Other settings go in `shiron.conf` next to the program: copy [shiron.conf.example](shiron.conf.example) and edit it. Real environment variables override the file.

Don't type the token on the command line (`$env:SHIRON_TOKEN = "..."`): shells save that in their history file.

| Variable | Default | Meaning |
|---|---|---|
| `SHIRON_TOKEN_FILE` | `token.txt` | File holding the bot token |
| `SHIRON_TOKEN` | | The token itself (overrides the file; avoid on shared machines) |
| `SHIRON_DB` | `shiron.db` | SQLite file path |
| `SHIRON_POLL` | `60s` | How often to check servers (minimum `10s`) |
| `SHIRON_CONNECT_URL` | (off) | Page for the **Join server** button, see below |
| `SHIRON_MAP_IMAGES` | ghostcap-gaming/cs2-map-images | Map picture URL with one `%s` for the map name, or `none` |

### Join server button

Discord buttons can only open `https://` links, not `steam://connect`. So Shiron links to a tiny page, [docs/connect.html](docs/connect.html), which opens Steam and joins the server.

1. Push this repo to GitHub, then go to **Settings → Pages**. Set the source to the `main` branch and the `/docs` folder.
2. Set `SHIRON_CONNECT_URL=https://<your-github-name>.github.io/<repo-name>/connect.html` (the official bot uses `https://kianyaa.github.io/Shiron/connect.html`).

Without it, Shiron still shows the `connect IP:port` console command.

### Map pictures

By default, pictures come from the community [cs2-map-images](https://github.com/ghostcap-gaming/cs2-map-images) collection. Official maps like `de_dust2` are covered. Custom maps (ZE, surf and similar) are looked up on Steam Workshop, and admins can link their own Workshop collections or set a picture for any map in `/servers list`. To use your own picture host instead, host PNGs named after the map and point `SHIRON_MAP_IMAGES` at them, e.g. `https://example.com/maps/%s.png`.

Check a CS2 server without Discord:

```bash
./shiron query 1.2.3.4:27015
```

## 3. Deploy

**Free hosting, step by step:** [deploy/ORACLE.md](deploy/ORACLE.md) sets Shiron up on Oracle Cloud's Always Free Arm server with one command, including daily backups. The manual steps below work on any Linux server.

Build for a Linux server from any machine (no C compiler needed):

```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o shiron-linux-arm64 .   # Oracle Ampere, Raspberry Pi
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o shiron-linux-amd64 .   # most VPS
```

Copy the file to `/opt/shiron/` on the server. Create a `shiron` user that owns that folder, and save the token so only that user can read it:

```bash
sudo useradd --system --home /opt/shiron shiron
sudo chown -R shiron: /opt/shiron
sudo -u shiron sh -c 'umask 077; cat > /opt/shiron/token.txt'   # paste the token, then Ctrl+D
```

Run it with systemd (`/etc/systemd/system/shiron.service`). The token stays out of this file, which every user can read:

```ini
[Unit]
Description=Shiron Discord bot
After=network-online.target

[Service]
ExecStart=/opt/shiron/shiron
WorkingDirectory=/opt/shiron
Restart=always
User=shiron

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now shiron
```

Or with Docker. The token file is mounted read-only rather than passed with `-e`, which `docker inspect` would show. The container runs as user 65532, so give that user the file:

```bash
sudo chown 65532 /opt/shiron/token.txt
docker build -t shiron .
docker run -d --restart=always -v shiron-data:/data \
  -v /opt/shiron/token.txt:/run/token:ro -e SHIRON_TOKEN_FILE=/run/token shiron
```

Only outbound internet access is needed; no ports have to be opened. Back up `shiron.db` to keep your settings.

## Notes

- One process serves every Discord server. Discord requires sharding above ~2,500 servers; Shiron doesn't shard yet.
- Discord asks bot owners to verify their identity once the bot is in 100 servers (you can apply at 75).
- The [Privacy Policy](https://kianyaa.github.io/Shiron/privacy.html) and [Terms of Service](https://kianyaa.github.io/Shiron/terms.html) are served by GitHub Pages from `docs/`, next to `connect.html`. They are linked in the Developer Portal under **General Information**. If you host your own copy, change the contact link inside both pages.

## Star history

[![Star history chart](https://api.star-history.com/svg?repos=Kianyaa/Shiron&type=Date)](https://star-history.com/#Kianyaa/Shiron&Date)

## License

Copyright (C) 2026 Kianyaa

Shiron is free software under the **GNU Affero General Public License v3.0** ([LICENSE](LICENSE)). In short:

- You may use, study, change and share it.
- If you run a **modified** Shiron for other people (for example, as a public Discord bot), you must make your changed source code available to them under the same license. Running it unmodified needs nothing extra.
- It comes with no warranty.

The full license text is what counts; this summary is only a guide.

### Name and logo

The license covers the code, not Shiron's identity. The name **Shiron** and its avatar belong to the project. If you run a modified version publicly, give it a different name and avatar, so people don't confuse it with the official bot. Saying "based on Shiron" with a link here is welcome.

### Contributing

By sending changes to this project, you agree they're released under the AGPL v3 and that the maintainer may also offer the project under other license terms.
