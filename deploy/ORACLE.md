# Host Shiron on Oracle Cloud (free)

Oracle's **Always Free** plan includes an Arm server with up to 4 CPUs and 24 GB of memory, free with no time limit. Shiron needs a small part of it: 1 CPU and 6 GB can track thousands of CS2 servers.

## 1. Create the account

1. Sign up at https://signup.cloud.oracle.com.
2. Pick a **home region** close to the CS2 servers you track (for Thailand: *Singapore*). You can't change it later, and free servers can only be made there.
3. Oracle asks for a card to check you're a real person. Always Free use isn't charged.

> **Tip:** Oracle may reclaim Always Free servers that sit almost idle for 7 days. Shiron is light, so to be safe, upgrade the account to **Pay As You Go** (Billing → Upgrade). You still pay nothing while you stay within the free limits; set a budget alert of $1 to be sure.

## 2. Create the server

In the Oracle Cloud console: **Compute → Instances → Create instance**.

| Setting | Choose |
|---|---|
| Name | `shiron` |
| Image | **Canonical Ubuntu 24.04** |
| Shape | **Ampere → VM.Standard.A1.Flex**, 1 OCPU, 6 GB memory (Always Free) |
| Networking | Keep the default new network, with a **public IPv4 address** |
| SSH keys | **Generate a key pair** and click **Save private key** (keep this file safe) |

Click **Create**. When it's *Running*, copy the **Public IP address**.

If you see **"Out of capacity"**, try another *Availability domain* in the same window, or try again later; free Arm servers are popular.

Shiron only makes outgoing connections, so no firewall ports need to be opened.

## 3. Install Shiron

On your PC, **stop `shiron.exe` first**: two copies with the same token would answer every command twice. Then, from the Shiron folder:

```powershell
.\deploy\deploy.ps1 -Server <public IP> -Key <path to the saved private key> -WithDatabase
```

- It builds the Linux program, uploads it with your `shiron.conf`, and installs it as a service that starts with the server and restarts if it ever stops.
- `-WithDatabase` moves your current tracked servers and alerts. Leave it out to start empty.
- It asks you to paste the bot token. Typing is hidden, and the token is saved only on the server.
- A database backup is made every day and kept for 14 days in `/opt/shiron/backups`.

If Windows says the key file's permissions are too open, run this once:

```powershell
icacls <path to key> /inheritance:r /grant:r "$($env:USERNAME):R"
```

## Updating

After changing the code, run the same command without `-WithDatabase`. It replaces the program and keeps the data and token.

## Useful commands on the server

Connect with `ssh -i <key> ubuntu@<public IP>`, then:

| What | Command |
|---|---|
| Live log | `sudo journalctl -u shiron -f` |
| Status | `systemctl status shiron` |
| Restart | `sudo systemctl restart shiron` |
| Backups | `ls /opt/shiron/backups` |
| Change settings | `sudo nano /opt/shiron/shiron.conf`, then restart |
