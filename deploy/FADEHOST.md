# Host Shiron on FadeHost (free, no card)

FadeHost's free plan runs one Discord bot 24/7 with 256 MB of memory, no credit card and nothing to renew. It builds Shiron straight from this GitHub repository.

**Limit to know:** free bots can only reach game servers on the Steam query ports **27015–27030**. A CS2 server on another port shows as offline. FadeHost's $2/month plan has no such limit.

## 1. Sign up

Create an account at https://fadehost.com and open the verification email.

## 2. Create the bot

In the panel: **Bots → Host a bot → Discord bot → your own repository**.

- **Repository:** `Kianyaa/Shiron`, branch `main`
- **Discord bot token:** paste the token. FadeHost passes it to Shiron as `DISCORD_TOKEN`.

Leave **Build command** and **Start command** empty: FadeHost compiles Go modules itself (`go build -o /data/bin/app .`) and starts the result.

## 3. Environment variables

On the bot's environment table, add these. The first three keep FadeHost's own compile step under the 256 MB limit; without them it can run out of memory.

| Name | Value | Why |
|---|---|---|
| `GOGC` | `20` | The compiler (and the bot) free memory sooner |
| `GOFLAGS` | `-p=1` | Compile one package at a time |
| `GOMEMLIMIT` | `150MiB` | Keeps memory well under 256 MB |
| `SHIRON_DB` | `/data/shiron.db` | `/data` is kept across deploys; the code folder is reset |
| `SHIRON_CONNECT_URL` | `https://kianyaa.github.io/Shiron/connect.html` | Join server button |
| `SHIRON_BIO` | (optional) your bot's profile text | |
| `SHIRON_LOADING_EMOJI` | (optional) like `<a:loading:123…>` | |

Save. The bot builds and starts; watch it log in on the **Console** tab.

## 4. Move your current data (optional)

1. Stop `shiron.exe` on your PC, so the database is complete and two bots don't run at once.
2. On FadeHost, stop the bot, open **Files**, and upload your `shiron.db` into `/data`.
3. Start the bot.

## Updating

Every push to `main` on GitHub rebuilds and restarts the bot. Your data in `/data` stays.
