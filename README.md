# Shiron

A small, fast Discord bot that tracks **CS2 servers**. One program file, no database server, speaks English, Thai and Simplified Chinese.

- Posts a message in a channel when a tracked server changes map, with a picture of the map and an optional minimum player count.
- `/map` and `/players` show live server info, with a **Join server** button that opens CS2 and connects.
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

Put the bot token in a file named `token.txt` next to the program (it is git-ignored), then:

```bash
go build -o shiron .
./shiron
```

On Windows: `go build -o shiron.exe .` then `.\shiron.exe`.

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
2. Set `SHIRON_CONNECT_URL=https://<your-github-name>.github.io/<repo-name>/connect.html`.

Without it, Shiron still shows the `connect IP:port` console command.

### Map pictures

By default, pictures come from the community [cs2-map-images](https://github.com/ghostcap-gaming/cs2-map-images) collection. Official maps like `de_dust2` are covered. Custom maps (ZE, surf and similar) usually aren't, so those posts have no picture. To use your own pictures, host PNGs named after the map and point `SHIRON_MAP_IMAGES` at them, e.g. `https://example.com/maps/%s.png`.

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
- Discord asks bot owners to verify their identity once the bot is in 100 servers.

## License

MIT
