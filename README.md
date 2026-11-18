# Shiron

A small, fast Discord bot that tracks **CS2 servers**. One program file, no database server, speaks English, Thai and Simplified Chinese.

- Posts a message in a channel when a tracked server changes map, with an optional minimum player count.
- `/map` and `/players` show live server info.
- `/notify` alerts users (by DM or a channel mention, their choice) when a map they want comes up. Wildcards like `de_*` work.

## Commands

| Command | Who |
|---|---|
| `/servers add address [channel] [min_players]` | Manage Server |
| `/servers list`, `/servers remove`, `/servers min-players` | Manage Server |
| `/map [server]`, `/players [server]` | everyone |
| `/notify add map [server]`, `/notify remove`, `/notify list`, `/notify mode` | everyone |
| `/ping`, `/help`, `/invite` | everyone |

Replies use each user's Discord language. Automatic map posts use the Discord server's language.

## 1. Create the Discord application

1. Open https://discord.com/developers/applications and click **New Application**. Name it **Shiron**.
2. **Bot** tab: click **Reset Token** and copy the token. It is the bot's password: never share it or commit it.
3. **Bot** tab: keep **Public Bot** on so anyone can invite it. Leave all **Privileged Gateway Intents** off; Shiron doesn't need them.

The invite link is printed when the bot starts, and `/invite` shows it too.

## 2. Run

Needs Go 1.27+ (only to build).

```bash
go build -o shiron .
SHIRON_TOKEN=your-token ./shiron
```

On Windows PowerShell:

```powershell
go build -o shiron.exe .
$env:SHIRON_TOKEN = "your-token"; .\shiron.exe
```

| Variable | Default | Meaning |
|---|---|---|
| `SHIRON_TOKEN` | (required) | Bot token |
| `SHIRON_DB` | `shiron.db` | SQLite file path |
| `SHIRON_POLL` | `60s` | How often to check servers (minimum `10s`) |

Check a CS2 server without Discord:

```bash
./shiron query 1.2.3.4:27015
```

## 3. Deploy

Build for a Linux server from any machine (no C compiler needed):

```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o shiron-linux-arm64 .   # Oracle Ampere, Raspberry Pi
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o shiron-linux-amd64 .   # most VPS
```

Copy the file to the server and run it with systemd (`/etc/systemd/system/shiron.service`):

```ini
[Unit]
Description=Shiron Discord bot
After=network-online.target

[Service]
ExecStart=/opt/shiron/shiron
WorkingDirectory=/opt/shiron
Environment=SHIRON_TOKEN=your-token
Restart=always
User=shiron

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now shiron
```

Or with Docker:

```bash
docker build -t shiron .
docker run -d --restart=always -e SHIRON_TOKEN=your-token -v shiron-data:/data shiron
```

Only outbound internet access is needed; no ports have to be opened. Back up `shiron.db` to keep your settings.

## Notes

- One process serves every Discord server. Discord requires sharding above ~2,500 servers; Shiron doesn't shard yet.
- Discord asks bot owners to verify their identity once the bot is in 100 servers.

## License

MIT
