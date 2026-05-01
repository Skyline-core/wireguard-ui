![](https://github.com/ngoduykhanh/wireguard-ui/workflows/wireguard-ui%20build%20release/badge.svg)

# wireguard-ui

## WireGuard UI v2

This repository ships **version 2** of the WireGuard UI: an updated shell-style layout, richer monitoring and administration pages, Passkeys (WebAuthn) support, bilingual UI (English / Spanish via `locale/en.json` and `locale/es.json`), and extended optional OS integration (sysctl, `wg-quick` / `wg syncconf`, log tail) while keeping the same core purpose as the upstream project—manage peers, generate configs, and distribute them by QR, file, email, or Telegram.

**Notes**

- **Building from source**: run `./prepare_assets.sh` before `go build` when templates or static assets change (see **Build** below).
- **Changing UI language**: set **Language** under **Global settings**, save, click **Apply config** in the toolbar, then **reload the page** so server-rendered templates and client-side `WG_T` strings refresh.

---

A web user interface to manage your WireGuard setup.

## Features

### Classic capabilities (upstream parity)

- Web UI to manage WireGuard peers (create, edit, enable/disable, remove).
- Richer client records (name, email, notes, subnet ranges, Telegram user id, etc.).
- Distribute configs via **QR code**, **download**, **email**, or **Telegram**.
- Global defaults (endpoint, DNS, MTU, keepalive, config path) and server interface editor.
- Optional **Apply config** workflow to write `wg.conf` and optionally reload the kernel (`wg-quick` / `wg syncconf` when enabled).

### New in v2

- **Shell layout**: fixed sidebar + main content (`wgshell.css`), mobile-friendly nav, unified top bar with **Apply config** / pending-change handling.
- **Dashboard**: at-a-glance server/client KPIs, WireGuard presence, and actions that match live data (`/api/dashboard-stats`, restart helpers where configured).
- **Traffic**: bandwidth view backed by cached WireGuard counter samples (`/api/wg-traffic-series`), range presets, peer-aware charts.
- **Logs**: live sections when enabled (global “Logs” toggle)—optional file tail (`WGUI_LOG_TAIL_PATH`), `systemctl` / `journalctl` snippets for `wg-quick@…`, periodic refresh from `/api/system-logs`.
- **Status**: read-only peer table from `wgctrl` for quick inspection.
- **Global settings (expanded)**: configurable **session idle timeout** (minutes), **Passkeys** master toggle, **UI theme** (dark / light / auto), **UI language** (English / Spanish), **realtime stats** gate for Logs/Dashboard polling; staged save + apply flow with localStorage dirty tracking.
- **Internationalization**: strings in `locale/en.json` and `locale/es.json`; templates use `tr` / client bundle `WG_T` + `wgT()` for JS toasts and dynamic UI.
- **Multi-user auth**: **Users** admin page—create/edit/delete users, admin role, suspend account, revoke all sessions, inline Passkey add/remove/rename per user.
- **My account / Profile**: self-service display name, email, password change, own Passkeys.
- **Passkeys (WebAuthn)**: passwordless sign-in and registration flows; env knobs for RP ID / origins behind reverse proxies (`WGUI_WEBAUTHN_*`).
- **Server page extras** (Linux, when allowed): optional **IPv4 forwarding** via `sysctl`, **persist** / **auto-apply** preferences, **`wg-quick` down/up/restart** and **`wg syncconf`** after apply, optional **systemd**-based restarts.
- **Wake-on-LAN**: manage hosts and send magic packets from the UI.
- **Client list UX**: card layout with inline enable toggle, traffic chips fed by **`/api/wg-peer-stats`**, and “Apply config” integration after edits.

![WireGuard UI v2](https://github.com/user-attachments/assets/b4454f2d-21ae-4d36-89b6-19c1260a930b)

## Run WireGuard-UI

> ⚠️The default username and password are `admin`. Please change it to secure your setup.

### Using binary file

Download the binary file from the release page and run it directly on the host machine

```
./wireguard-ui
```

### Using docker compose

The [examples/docker-compose](examples/docker-compose) folder contains example docker-compose files.
Choose the example which fits you the most, adjust the configuration for your needs, then run it like below:

```
docker-compose up
```

## Environment Variables

| Variable                      | Description                                                                                                                                                                                                                                                                         | Default                            |
|-------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------------------|
| `BASE_PATH`                   | Set this variable if you run wireguard-ui under a subpath of your reverse proxy virtual host (e.g. /wireguard)                                                                                                                                                                      | N/A                                |
| `BIND_ADDRESS`                | The addresses that can access to the web interface and the port, use unix:///abspath/to/file.socket for unix domain socket.                                                                                                                                                         | 0.0.0.0:80                         |
| `SESSION_SECRET`              | The secret key used to encrypt the session cookies. Set this to a random value                                                                                                                                                                                                      | N/A                                |
| `SESSION_SECRET_FILE`         | Optional filepath for the secret key used to encrypt the session cookies. Leave `SESSION_SECRET` blank to take effect                                                                                                                                                               | N/A                                |
| `SESSION_MAX_DURATION`        | Max time in days a remembered session is refreshed and valid. Non-refreshed session is valid for 7 days max, regardless of this setting.                                                                                                                                            | 90                                 |
| `SUBNET_RANGES`               | The list of address subdivision ranges. Format: `SR Name:10.0.1.0/24; SR2:10.0.2.0/24,10.0.3.0/24` Each CIDR must be inside one of the server interfaces.                                                                                                                           | N/A                                |
| `WGUI_USERNAME`               | The username for the login page. Used for db initialization only                                                                                                                                                                                                                    | `admin`                            |
| `WGUI_PASSWORD`               | The password for the user on the login page. Will be hashed automatically. Used for db initialization only                                                                                                                                                                          | `admin`                            |
| `WGUI_PASSWORD_FILE`          | Optional filepath for the user login password. Will be hashed automatically. Used for db initialization only. Leave `WGUI_PASSWORD` blank to take effect                                                                                                                            | N/A                                |
| `WGUI_PASSWORD_HASH`          | The password hash for the user on the login page. (alternative to `WGUI_PASSWORD`). Used for db initialization only                                                                                                                                                                 | N/A                                |
| `WGUI_PASSWORD_HASH_FILE`     | Optional filepath for the user login password hash. (alternative to `WGUI_PASSWORD_FILE`). Used for db initialization only. Leave `WGUI_PASSWORD_HASH` blank to take effect                                                                                                         | N/A                                |
| `WGUI_ENDPOINT_ADDRESS`       | The default endpoint address used in global settings where clients should connect to. The endpoint can contain a port as well, useful when you are listening internally on the `WGUI_SERVER_LISTEN_PORT` port, but you forward on another port (ex 9000). Ex: myvpn.dyndns.com:9000 | Resolved to your public ip address |
| `WGUI_FAVICON_FILE_PATH`      | The file path used as website favicon                                                                                                                                                                                                                                               | Embedded WireGuard logo            |
| `WGUI_DNS`                    | The default DNS servers (comma-separated-list) used in the global settings                                                                                                                                                                                                          | `1.1.1.1`                          |
| `WGUI_MTU`                    | The default MTU used in global settings                                                                                                                                                                                                                                             | `1450`                             |
| `WGUI_PERSISTENT_KEEPALIVE`   | The default persistent keepalive for WireGuard in global settings                                                                                                                                                                                                                   | `15`                               |
| `WGUI_FIREWALL_MARK`          | The default WireGuard firewall mark                                                                                                                                                                                                                                                 | `0xca6c`  (51820)                  |
| `WGUI_TABLE`                  | The default WireGuard table value settings                                                                                                                                                                                                                                          | `auto`                             |
| `WGUI_CONFIG_FILE_PATH`       | The default WireGuard config file path used in global settings                                                                                                                                                                                                                      | `/etc/wireguard/wg0.conf`          |
| `WGUI_LOG_LEVEL`              | The default log level. Possible values: `DEBUG`, `INFO`, `WARN`, `ERROR`, `OFF`                                                                                                                                                                                                     | `INFO`                             |
| `WG_CONF_TEMPLATE`            | The custom `wg.conf` config file template. Please refer to our [default template](https://github.com/ngoduykhanh/wireguard-ui/blob/master/templates/wg.conf)                                                                                                                        | N/A                                |
| `EMAIL_FROM_ADDRESS`          | The sender email address                                                                                                                                                                                                                                                            | N/A                                |
| `EMAIL_FROM_NAME`             | The sender name                                                                                                                                                                                                                                                                     | `WireGuard UI`                     |
| `SENDGRID_API_KEY`            | The SendGrid api key                                                                                                                                                                                                                                                                | N/A                                |
| `SENDGRID_API_KEY_FILE`       | Optional filepath for the SendGrid api key. Leave `SENDGRID_API_KEY` blank to take effect                                                                                                                                                                                           | N/A                                |
| `SMTP_HOSTNAME`               | The SMTP IP address or hostname                                                                                                                                                                                                                                                     | `127.0.0.1`                        |
| `SMTP_PORT`                   | The SMTP port                                                                                                                                                                                                                                                                       | `25`                               |
| `SMTP_USERNAME`               | The SMTP username                                                                                                                                                                                                                                                                   | N/A                                |
| `SMTP_PASSWORD`               | The SMTP user password                                                                                                                                                                                                                                                              | N/A                                |
| `SMTP_PASSWORD_FILE`          | Optional filepath for the SMTP user password. Leave `SMTP_PASSWORD` blank to take effect                                                                                                                                                                                            | N/A                                |
| `SMTP_AUTH_TYPE`              | The SMTP authentication type. Possible values: `PLAIN`, `LOGIN`, `NONE`                                                                                                                                                                                                             | `NONE`                             |
| `SMTP_ENCRYPTION`             | The encryption method. Possible values: `NONE`, `SSL`, `SSLTLS`, `TLS`, `STARTTLS`                                                                                                                                                                                                  | `STARTTLS`                         |
| `SMTP_HELO`                   | Hostname to use for the HELO message. smtp-relay.gmail.com needs this set to anything but `localhost`                                                                                                                                                                               | `localhost`                        |
| `TELEGRAM_TOKEN`              | Telegram bot token for distributing configs to clients                                                                                                                                                                                                                              | N/A                                |
| `TELEGRAM_ALLOW_CONF_REQUEST` | Allow users to get configs from the bot by sending a message                                                                                                                                                                                                                        | `false`                            |
| `TELEGRAM_FLOOD_WAIT`         | Time in minutes before the next conf request is processed                                                                                                                                                                                                                           | `60`                               |

### Session idle timeout (`Configuración` → Sesión y seguridad)

In the UI, **Tiempo de sesión (minutos)** is stored as `session_timeout_minutes` (integer). **Always use whole minutes—not seconds.**

| Item | Detail |
|------|--------|
| **Unit** | **Minutes**, range **5–1440** (about 24 h max). Example: enter `30` for ~30 minutes. |
| **Behavior** | **Idle logout:** after no authenticated HTTP request for longer than this time, the session is invalid (each request resets the idle clock). Applies to browsing and API endpoints that enforce `ValidSession`. |
| **When it applies** | After saving from **Settings** and confirming **Aplicar config**, new sessions use this value when users **log in again**. Log out or wait for expiry to observe the change immediately. |
| **Remember-me** | If a finite timeout is set in global settings, the login checkbox no longer lengthens the session to 7 days. |
| **`SESSION_MAX_DURATION`** | Separate hard cap on how long any session identity may persist (days from login), independent of idle timeout. See the env table above. |

### Defaults for server configuration

These environment variables are used to control the default server settings used when initializing the database.

| Variable                          | Description                                                                                   | Default         |
|-----------------------------------|-----------------------------------------------------------------------------------------------|-----------------|
| `WGUI_SERVER_INTERFACE_ADDRESSES` | The default interface addresses (comma-separated-list) for the WireGuard server configuration | `10.252.1.0/24` |
| `WGUI_SERVER_LISTEN_PORT`         | The default server listen port                                                                | `51820`         |
| `WGUI_SERVER_POST_UP_SCRIPT`      | The default server post-up script                                                             | N/A             |
| `WGUI_SERVER_POST_DOWN_SCRIPT`    | The default server post-down script                                                           | N/A             |

### Defaults for new clients

These environment variables are used to set the defaults used in `New Client` dialog.

| Variable                                    | Description                                                                                     | Default     |
|---------------------------------------------|-------------------------------------------------------------------------------------------------|-------------|
| `WGUI_DEFAULT_CLIENT_ALLOWED_IPS`           | Comma-separated-list of CIDRs for the `Allowed IPs` field. (default )                           | `0.0.0.0/0` |
| `WGUI_DEFAULT_CLIENT_EXTRA_ALLOWED_IPS`     | Comma-separated-list of CIDRs for the `Extra Allowed IPs` field. (default empty)                | N/A         |
| `WGUI_DEFAULT_CLIENT_USE_SERVER_DNS`        | Boolean value [`0`, `f`, `F`, `false`, `False`, `FALSE`, `1`, `t`, `T`, `true`, `True`, `TRUE`] | `true`      |
| `WGUI_DEFAULT_CLIENT_ENABLE_AFTER_CREATION` | Boolean value [`0`, `f`, `F`, `false`, `False`, `FALSE`, `1`, `t`, `T`, `true`, `True`, `TRUE`] | `true`      |

### Docker only

These environment variables only apply to the docker container.

| Variable              | Description                                                   | Default |
|-----------------------|---------------------------------------------------------------|---------|
| `WGUI_MANAGE_START`   | Start/stop WireGuard when the container is started/stopped    | `false` |
| `WGUI_MANAGE_RESTART` | Auto restart WireGuard when we Apply Config changes in the UI | `false` |

### Servidor UI (optional OS integration)

Gate optional privileged actions invoked from the **Servidor** page (binary or Docker—the process must run on Linux with adequate permissions where needed):

| Variable | Description | Default |
|----------|-------------|---------|
| `WGUI_ALLOW_SYSCTL_IP_FORWARD` | When `true`, saving with **Reenvío IP (ip_forward)** may run `sysctl -w net.ipv4.ip_forward=1` / `...=0` on Linux. Without it, only the preference is stored in the database. Ignored outside Linux. | `false` |
| `WGUI_WG_SYNCCONF_AFTER_APPLY` | When `true`, **Aplicar config** runs **`wg-quick strip <conf> \| wg syncconf <iface>`** on Linux so the running WireGuard matches the written file (e.g. disabling a client removes its peer from the server without `wg-quick down/up`). Requires `wg` and `wg-quick` on `$PATH`. If unset or `false`, Apply only writes the file/hash and does not reload kernel state. | `false` |
| `WGUI_ALLOW_WG_QUICK` | When `true`, **Apply** can run `wg-quick` down/up and **Servidor** shows **Detener** / **Iniciar** / **Reiniciar**. If unset, wg-quick controls are **off**. Start with `WGUI_ALLOW_WG_QUICK=true` when you intend to restart the tunnel from the UI. Env values are trimmed before parsing. | `false` |
| `WGUI_WG_RESTART_VIA_SYSTEMD` | On Linux, **Apply** prefers `systemctl restart wg-quick@ifac` when that unit exists (`LoadState=loaded`), so **`journalctl -u wg-quick@wg0`** shows restarts like a manual systemd restart. If `false` or no systemd, uses `wg-quick down`/`up`. | `true` |
| `WGUI_WGCONF_PENDING_WHEN_TUNNEL_STOPPED` | Linux: when Apply does **not** restart WireGuard while the netdev is absent/down (e.g. after **Detener**), the UI writes a side file next to `wg.conf` (suffix `.wgui-pending`) instead of overwriting the live **`WGUI_CONFIG_FILE_PATH`**. That avoids systemd **`.path`** units watching `wg.conf` that restart `wg-quick` on every save. **`wg-quick up`** or **Servidor › Iniciar** merges the pending file into `wg.conf` first. Set `false` to always write `wg.conf` directly (legacy). | `true` |
| `WGUI_LOG_TAIL_PATH` | Optional absolute path to a log file shown in the **Logs** page. This variable is read-only: wireguard-ui does not write this file automatically. | _(unset)_ |
| `WGUI_WEBAUTHN_RP_ID` | Optional fixed WebAuthn RP ID (recommended behind reverse proxy/public domain). If unset, it is inferred from request host. | _(auto)_ |
| `WGUI_WEBAUTHN_RP_ORIGINS` | Optional comma-separated allowed origins for Passkeys (example: `https://vpn.example.com,https://admin.example.com`). If unset, origin is inferred per request. | _(auto)_ |
| `WGUI_WEBAUTHN_RP_DISPLAY_NAME` | Optional WebAuthn RP display name shown by authenticators. | `WireGuard UI` |

#### Troubleshooting: `wg-quick up` fails on `ip -6 route` / «Cannot find device wg0»

After toggling peers and **Iniciar**, a failed half-bridge can leave routing in an odd state; the UI now runs **`wg-quick down`** (ignored if already down), waits briefly, then **`wg-quick up`**, and **retries once** if the first `up` still errors. If it persists, exclude **`wg0`** from **NetworkManager** / **systemd-networkd**, and ensure IPv6 is consistent (either working or intentionally off) with the **`Address`** line in **`wg.conf`**.

#### `WGUI_LOG_TAIL_PATH` quick setup (systemd)

Use this when you want the **Logs** page to also show a custom application log file.

1. Add environment variable to your wireguard-ui service:

```ini
[Service]
Environment="WGUI_LOG_TAIL_PATH=/var/log/wireguard-ui.log"
```

2. Ensure file exists and is readable by the service user:

```bash
sudo touch /var/log/wireguard-ui.log
sudo chmod 640 /var/log/wireguard-ui.log
```

3. (Recommended) append service stdout/stderr to that file:

```ini
[Service]
StandardOutput=append:/var/log/wireguard-ui.log
StandardError=append:/var/log/wireguard-ui.log
```

4. Reload and restart:

```bash
sudo systemctl daemon-reload
sudo systemctl restart wireguard-ui
```

5. Verify:

```bash
sudo systemctl show wireguard-ui -p Environment
sudo tail -n 50 /var/log/wireguard-ui.log
```

> Note: The Logs page now also includes `systemctl status wg-quick@<iface>` and recent `journalctl` output. `WGUI_LOG_TAIL_PATH` is only for the optional file section.

#### Passkeys (WebAuthn) behind reverse proxy (systemd example)

If you use a public domain and/or reverse proxy (Nginx, Caddy, Traefik, Cloudflare Tunnel), define a fixed WebAuthn RP ID and allowed origins:

```ini
[Service]
Environment="WGUI_WEBAUTHN_RP_ID=vpn.example.com"
Environment="WGUI_WEBAUTHN_RP_ORIGINS=https://vpn.example.com"
Environment="WGUI_WEBAUTHN_RP_DISPLAY_NAME=WireGuard UI"
```

Then reload and restart:

```bash
sudo systemctl daemon-reload
sudo systemctl restart wireguard-ui
```

Notes:
- `WGUI_WEBAUTHN_RP_ID` must match your effective login domain.
- `WGUI_WEBAUTHN_RP_ORIGINS` accepts comma-separated values for multi-origin setups.
- Passkeys require `https://` in production (browsers only allow non-HTTPS for localhost).

##### Caddy + Dynamic DNS (No-IP): quick HTTPS so Passkeys work

Browsers treat Passkeys/WebAuthn as **[secure context](https://developer.mozilla.org/en-US/docs/Web/Security/Secure_Contexts)** (`https://` on a hostname, or `http://localhost`). Plain `http://<tu-ip>` is **not** enough. Use a hostname (No-IP, DuckDNS, etc.), forward ports **80** and **443**, and terminate TLS with Caddy.

1. **No-IP (or similar)**  
   Create `tuhost.ddns.net` (example), install the updater or rely on No-IP so the **A record** points to your **WAN** public IP.

2. **Firewall / router**  
   Forward **TCP 80** and **TCP 443** from the internet to the machine that runs Caddy (required for Let’s Encrypt HTTP-01 by default).

3. **Install Caddy**  
   Follow [Caddy install docs](https://caddyserver.com/docs/install) for your distro (official repo or package).

4. **`Caddyfile`** (minimal reverse proxy to WireGuard UI on loopback):

   ```caddyfile
   tuhost.ddns.net {
       encode gzip
       reverse_proxy 127.0.0.1:5000
   }
   ```

   Replace `tuhost.ddns.net` with your hostname and **`5000`** with the port where `wireguard-ui` listens (`BIND_ADDRESS`, e.g. `:5000` or `127.0.0.1:5000`).

5. **(Optional, recommended)** Listen only on localhost so only Caddy exposes HTTPS:

   ```bash
   BIND_ADDRESS=127.0.0.1:5000 ./wireguard-ui
   ```

6. **Restart Caddy**, then open **`https://tuhost.ddns.net`** and confirm the browser shows a **valid lock** (no certificate warnings).

7. **`wireguard-ui` systemd** — set RP ID/origin to match **exactly** what users type in the browser:

   ```ini
   [Service]
   Environment="WGUI_WEBAUTHN_RP_ID=tuhost.ddns.net"
   Environment="WGUI_WEBAUTHN_RP_ORIGINS=https://tuhost.ddns.net"
   ```

   Then `daemon-reload` and `restart wireguard-ui`.

8. **Inside the UI** — **Configuración** → enable **Passkeys** → **Aplicar config**. Then **Administración → Usuarios**: register a passkey per user. Login page will offer **Entrar con Passkey** once enabled.

If HTTPS still fails behind NAT, verify port 80 reaches Caddy on first certificate issuance; use `journalctl -u caddy -f` on errors.

## Auto restart WireGuard daemon

WireGuard-UI only takes care of configuration generation. On Linux you can enable in-process `wg syncconf` after apply (see variables above), or use systemd to watch for changes and restart the
service. Following is an example:

> **Note:** The **systemd** block below does **not** start the `wireguard-ui` web process. It only runs `systemctl restart wg-quick@wg0` when `wg0.conf` is modified on disk. The UI binary is a separate program (see **Run WireGuard-UI** above and **systemd unit for `wireguard-ui`** below).

### systemd unit for `wireguard-ui` (web app)

The app stores its JSON database under **`./db` relative to the process working directory**, so the unit should set `WorkingDirectory` to a folder you own (e.g. `/var/lib/wireguard-ui`) and place the binary on your `PATH` or use an absolute `ExecStart`.

Example `/etc/systemd/system/wireguard-ui.service`:

```ini
[Unit]
Description=WireGuard UI
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=wireguard-ui
Group=wireguard-ui
WorkingDirectory=/var/lib/wireguard-ui
Environment="BIND_ADDRESS=127.0.0.1:5000"
# Optional: EnvironmentFile=-/etc/wireguard-ui.env
ExecStart=/usr/local/bin/wireguard-ui
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Create the data directory and user as needed (names are examples), then `systemctl daemon-reload`, `systemctl enable --now wireguard-ui`.

### Using systemd (restart `wg-quick` when config file changes)

Create `/etc/systemd/system/wgui.service`

```bash
cd /etc/systemd/system/
cat << EOF > wgui.service
[Unit]
Description=Restart WireGuard
After=network.target

[Service]
Type=oneshot
ExecStart=/usr/bin/systemctl restart wg-quick@wg0.service

[Install]
RequiredBy=wgui.path
EOF
```

Create `/etc/systemd/system/wgui.path`

```bash
cd /etc/systemd/system/
cat << EOF > wgui.path
[Unit]
Description=Watch /etc/wireguard/wg0.conf for changes

[Path]
PathModified=/etc/wireguard/wg0.conf

[Install]
WantedBy=multi-user.target
EOF
```

Apply it

```sh
systemctl enable wgui.{path,service}
systemctl start wgui.{path,service}
```

### Using openrc

Create `/usr/local/bin/wgui` file and make it executable

```sh
cd /usr/local/bin/
cat << EOF > wgui
#!/bin/sh
wg-quick down wg0
wg-quick up wg0
EOF
chmod +x wgui
```

Create `/etc/init.d/wgui` file and make it executable

```sh
cd /etc/init.d/
cat << EOF > wgui
#!/sbin/openrc-run

command=/sbin/inotifyd
command_args="/usr/local/bin/wgui /etc/wireguard/wg0.conf:w"
pidfile=/run/${RC_SVCNAME}.pid
command_background=yes
EOF
chmod +x wgui
```

Apply it

```sh
rc-service wgui start
rc-update add wgui default
```

### Using Docker

Set `WGUI_MANAGE_RESTART=true` to manage Wireguard interface restarts.
Using `WGUI_MANAGE_START=true` can also replace the function of `wg-quick@wg0` service, to start Wireguard at boot, by
running the container with `restart: unless-stopped`. These settings can also pick up changes to Wireguard Config File
Path, after restarting the container. Please make sure you have `--cap-add=NET_ADMIN` in your container config to make
this feature work.

## Build

### Build docker image

Go to the project root directory and run the following command:

```sh
docker build --build-arg=GIT_COMMIT=$(git rev-parse --short HEAD) -t wireguard-ui .
```

or

```sh
docker compose build --build-arg=GIT_COMMIT=$(git rev-parse --short HEAD)
```

:information_source: A container image is available on [Docker Hub](https://hub.docker.com/r/ngoduykhanh/wireguard-ui)
which you can pull and use

```
docker pull ngoduykhanh/wireguard-ui
````

### Build binary file

Prepare the assets directory

```sh
./prepare_assets.sh
```

Then build your executable

```sh
go build -o wireguard-ui
```

## License

MIT. See [LICENSE](https://github.com/ngoduykhanh/wireguard-ui/blob/master/LICENSE).

## Support

If you like the project and want to support it, you can *buy me a coffee* ☕

<a href="https://www.buymeacoffee.com/khanhngo" target="_blank"><img src="https://cdn.buymeacoffee.com/buttons/default-orange.png" alt="Buy Me A Coffee" height="41" width="174"></a>
