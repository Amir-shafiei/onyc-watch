# ONyc Watch

An English Telegram bot, written in Go, for Exponent ONyc yield markets and USDC borrowing liquidity on Kamino and Loopscale. No wallet, signature or seed phrase is needed.

## Version 1.8

- **Current AUM:** OnRe's live USD AUM, cached in the background with fetch time and stale/unavailable labels.
- **My Alerts:** up to 30 independent APY targets and maturity reminders per user, with individual edit, pause/resume and delete controls.
- **Above / Below:** explicit inclusive target direction; a successfully delivered APY target removes only that alert.
- **Maturity Reminders:** opt-in market reminders at 7 days and 1 day remaining. Joining within the final day sends only the 1-day reminder.
- **New Market Alerts:** opt-in discovery notifications for Exponent, Kamino and Loopscale. Each provider's initial successful snapshot is silently baselined. An outage does not reset that baseline.

Commands added: `/aum`, `/alerts`, `/maturities`, `/newmarkets`. Existing v1.7 subscriptions remain accessible through My Alerts. Global Pause applies to all notifications. An already-sending message may still arrive. New APY rules are independent of My Markets display filters.

The AUM source is `https://core.api.onre.finance/data/live-tvl`, also used by the official OnRe frontend. Fetch time is not the upstream valuation time. Market discovery means first seen by this bot, not a verified launch timestamp. Maturity reminders describe a selected market, not a connected wallet position.

### Update an existing Ubuntu installation

From the source checkout, fetch the new code and run `bash server/update.sh`. Do not rerun the fresh-install script. The updater tests and builds first, then stops the service briefly to back up its executable, token, service file and state under `/var/backups/onyc-watch/`. It preserves the existing production token and user state, validates the new process for 30 seconds, and attempts to restore the previous executable and state if startup fails. It does not prove external APIs or Telegram are reachable: also test the bot in Telegram after updating.

```sh
cd /opt/onyc-watch-src
git pull --ff-only origin main
bash server/update.sh
```

If the first installation needed a manual CRLF fix, save those local script changes with `git stash push -m before-v1.8-update -- server/install.sh server/onyc-watch.service` before pulling. The repository now enforces LF line endings for Linux scripts.

## Start on Windows

1. Create a bot using the official [@BotFather](https://t.me/BotFather) and copy its token.
2. Open `Start-Bot.cmd` in this folder. On first launch, it prompts for the token with hidden input and saves it locally in `.env`.
3. Open your new bot in Telegram and send `/start`.
4. Keep the terminal running. Closing it stops monitoring. Use Ctrl+C for a graceful shutdown.

The downloadable Windows package includes `bin/onyc-watch-v1.7.exe` and requires no Go installation. A source checkout from GitHub requires Go 1.24 or newer. The PowerShell launcher reuses an already-enabled Windows HTTPS proxy; it does not change Windows network settings. Explicit `HTTPS_PROXY` environment settings take precedence. The executable itself reads standard proxy environment variables, not the Windows registry.

To inspect live data without a Telegram token:

```powershell
.\Start-Bot.ps1 -Check
.\Start-Bot.ps1 -Preview
```

If script execution is restricted, run `Start-Bot.cmd -Check` or `Start-Bot.cmd -Preview`.

## Bot buttons

| Button | Result |
|---|---|
| Current APY | Current provider snapshot for YT ONyc, YT srONyc, srONyc and jrONyc |
| USDC Availability | Separate collateral/debt markets on Kamino and Loopscale |
| Alert Settings | APY threshold, change in percentage points, minimum USDC, optional maximum borrow APY |
| My Markets | Toggle products, platforms and individual markets/maturities |
| Refresh | Request a background refresh; display cached rates immediately |
| Pause / Resume | Toggle automatic alerts |
| Data Status | Provider health and fetch times |

Commands: `/start`, `/help`, `/apy`, `/usdc`, `/refresh`, `/settings`, `/markets`, `/status`, `/stop`, `/resume`, `/cancel`, `/delete`.

Settings conversations accept plain numbers; APY and maximum borrow APY also accept `off`. `/delete` removes that chat's settings and alert history. Groups and channels are ignored; this version supports private chats.

## What the numbers mean

- **YT ONyc / YT srONyc:** market implied APY and underlying APY are shown separately. Implied APY is NOT a buyer's realized or promised YT return. The bot does not invent a leveraged YT APY. YT price, maturity and any API-reported holder-reward APY are shown separately.
- **srONyc / jrONyc:** current marginal tranche APY estimates published by Exponent, converted from ratios to percentages. These are variable estimates, not guaranteed fixed returns. Points are excluded.
- **Kamino:** the official collateral/debt-pair endpoint identifies supported routes. Only active USDC reserves with a positive pair LTV are included. Capacity uses `actualAvailableLiquidity` and the applicable outside-elevation-group or collateral-specific cap. It is not `totalSupply - totalBorrow`, and it is not the maximum amount a particular wallet can borrow.
- **Loopscale:** `principalAmountAvailable / 1e6` is **reported loop capacity**, not an executable quote. `wAvgApy` is already in percentage units and describes existing positions. It is not an entry quote or a current borrowing rate. Shared liquidity must not be summed across rows.
- Loopscale borrow rates come from `/markets/quote`, separately from loop APY and reported loop capacity. The bot checks 1-day, 1-week, 1-month and 3-month offers. A rate filter requires a fresh single-offer capacity covering your minimum USDC amount. Rates are quoted terms, not guaranteed execution or a wallet-specific borrowing limit.
- Wallet eligibility, collateral deposit headroom, collateral amounts, swap slippage and complete loop execution are not simulated. The bot alerts on available reported data and links to the protocol for verification. It does not execute transactions.
- Expired Exponent markets and recognized expired PT Loopscale slugs are excluded. Markets and mints are discovered from providers, not fabricated. A YT/junior token is never assumed supported as collateral merely because its APY is tracked.

## Alerts and freshness

- Each provider refreshes independently in the background every 60 seconds. Current APY and USDC Availability read the in-memory cache and never wait for market API requests.
- Refresh requests are coalesced per provider for 10 seconds. The response shows saved data immediately and asks the user to tap Current APY again to see the completed refresh.
- The APY view no longer repeats Exponent links under each product. Fetch times and stale labels remain visible.
- APY alerts require an explicit market selection. Change mode tracks the configured cumulative change from the baseline/last delivered alert; the initial baseline is silent.
- An APY target waits for a fall to/below a lower target or a rise to/above a higher target, based on the fresh rate when saved. An equal target can fire immediately.
- When USDC alerts are enabled, the default minimum is 100 USDC reported available. First observation can trigger an alert. It rearms after a confirmed drop below the threshold, with a 15-minute per-market cooldown.
- Paused, muted, expired, failed or stale data does not generate alerts. A data outage does not count as zero liquidity and does not rearm an alert.
- Failed deliveries do not latch the alert as sent; a later polling cycle retries. Delivery is at-least-once: a crash between successful Telegram delivery and state persistence can duplicate a message.
- Old provider data is retained with a stale/unavailable label during outages. Loopscale's provider update timestamp is additionally checked. APIs without a computation timestamp are labeled with fetch time; a successful fetch cannot prove the upstream calculation was refreshed.
- The process uses a kernel-managed file lock and atomic state-file replacement. State survives restarts. The lock releases automatically after process exit/crash.

## Configuration

Copy `.env.example` to `.env` when not using the Windows setup prompt. Existing environment variables override `.env`.

| Variable | Default | Purpose |
|---|---|---|
| `TELEGRAM_BOT_TOKEN` | Required to run the bot | BotFather token; never commit/share it |
| `ALLOWED_USER_IDS` | Empty | Optional comma-separated Telegram user IDs; empty allows any private-chat user |
| `POLL_INTERVAL` | `60s` | Minimum allowed: 30 seconds |
| `STALE_AFTER` | `10m` | Must be at least the polling interval |
| `ALERT_COOLDOWN` | `15m` | Per-market alert cooldown |
| `DATA_DIR` | `data` | Writable persistent state directory |
| `HTTPS_PROXY` | Unset | Optional network proxy |

State contains Telegram chat IDs and preferences. It is stored in `data/state.json`; the token stays in `.env` or the process environment. HTTP errors are sanitized to avoid logging Telegram token URLs. Back up the data directory for recovery; malformed state fails closed instead of silently overwriting saved preferences.

## Build and test

Go 1.24+; standard library only, no third-party Go modules.

```sh
go test ./...
go vet ./...
go build -trimpath -o onyc-watch .
./onyc-watch -check
./onyc-watch -preview
./onyc-watch
```

`-check` prints live JSON and exits unsuccessfully if any provider fails. `-preview` prints the actual English messages without contacting Telegram. Test fixtures are captured public provider responses; they are used only by tests and are never a runtime fallback.

Tests cover live-response parsing, rate/USDC units, missing fields, maturity filtering, borrowing caps, alert deduplication and rearming, stale data, unknown-rate filtering, settings validation, mocked Telegram callbacks, secret redaction, state persistence and exclusive locking.

## Run continuously on a server

For a fresh Ubuntu VPS, clone this repository and run the included systemd installer. Ubuntu 24.04's base repository may contain an older Go release, so install the current Go package from Snap first:

```sh
apt-get update && apt-get install -y git snapd python3
snap install go --classic
git clone https://github.com/Amir-shafiei/onyc-watch.git /opt/onyc-watch-src
cd /opt/onyc-watch-src
cp .env.example server/upload.env
nano server/upload.env
bash server/install.sh
systemctl status onyc-watch --no-pager
```

Set `TELEGRAM_BOT_TOKEN` in `server/upload.env` before running the installer. The installer builds the current checkout, stores the token in `/etc/onyc-watch/bot.env` with mode `0600`, installs a restricted systemd service and removes the temporary upload configuration. Stop the Windows copy before starting the VPS copy because Telegram permits only one long-polling process per bot token.

Docker is also supported:

```sh
# Configure .env first; do not copy a localhost Windows proxy to a remote server.
docker compose up -d --build
docker compose logs -f
```

The Docker service runs as non-root, exposes no ports, uses outbound Telegram long polling, persists data in a named volume and restarts unless stopped. The Docker configuration is provided for deployment; building/running Docker is separate from the locally tested Windows executable. Use only one active polling process per bot token. An existing webhook must be removed before polling; the bot deliberately does not overwrite another deployment's webhook.

## Data-source contracts

Verified during development on 2026-10-02:

- Exponent frontend JSON: `https://app.exponent.finance/api/markets` and `/api/tranching-markets`. These are the public web application's endpoints, not a versioned API with a stability guarantee. Schema changes require adapter updates. [Yield market documentation](https://docs.exponent.finance/user-documentation/yield-markets), [tranche APY documentation](https://docs.exponent.finance/developer-tranching/typescript/read-functions/calculate-apy-and-protection).
- Kamino: `https://api.kamino.finance/markets/collateral-reserves` and `/reserves/{reserve}/stats`. [Official OpenAPI](https://api.kamino.finance/openapi/json?openapi=3.0.0).
- Loopscale: POST `https://tars.loopscale.com/v1/markets/loop/info` with `{}`. [Endpoint](https://docs.loopscale.com/api-reference/data/loops/get-loop-info), [metric definitions](https://docs.loopscale.com/api-reference/guides/vault-and-loop-metrics).
- [Telegram Bot API](https://core.telegram.org/bots/api).

This is an independent monitoring tool, not an official OnRe, Exponent, Kamino or Loopscale bot.

## Updating to v1.7

Stop the previous bot with Ctrl+C, then run Start-Bot.cmd again. The launcher prefers bin/onyc-watch-v1.7.exe. Your .env and data/state.json are unchanged. Do not run both versions simultaneously.

Callback acknowledgements run concurrently with replies. Alert delivery uses a bounded background worker while a separate long-poll request receives commands. User state remains owned by the event loop; successful alerts are rechecked against current preferences and conditions before recording delivery. This removes avoidable waits but does not guarantee a particular internet response time.

In Alert Settings, APY Alert opens the APY management panel with Edit and Disable / Enable. The separate Disable USDC Alerts button controls USDC notifications. Thresholds, market selections and alert history are retained across restarts. Manual Current APY and USDC views remain available. Pause / Resume remains a global override. A notification already being sent may still arrive. `/cancel` only exits a settings prompt; use Edit → APY Change to choose change mode.

## Selecting an APY alert market (v1.4)

Open APY Alert. If no market is configured, select an active market (including the specific YT maturity) and enter the threshold. Otherwise, the panel shows the market, condition and status, with Edit and Disable / Enable. Edit offers Threshold or APY Change before market selection. Each user follows one APY market at a time. Saving a different market replaces the previous APY selection; cancellation keeps the previous alert. Selecting a market also enables it in My Markets. Targets below the current rate wait for a fall to or below the target; higher targets wait for a rise to or above it. The direction is saved and shown in the APY panel. Change mode establishes a starting baseline instead.

APY settings preserve USDC delivery history, and USDC settings preserve APY history. An in-flight USDC receipt is not discarded merely because an APY setting changed. USDC monitoring remains independent and may alert separately for each enabled pool; use Disable USDC Alerts to stop it.

New users start with automatic alerts disabled. Completing APY setup enables the chosen APY alert; USDC notifications require Enable USDC Alerts. Existing USDC preferences are preserved. Older global APY configurations without a selected market are disabled on load until the user chooses a market; old threshold values are retained. Manual rate views are unaffected.

## One-time APY alerts (v1.6)

After Telegram confirms successful delivery, the APY alert is removed, including its selected market and target. This applies to both target and change modes. Failed delivery retains the alert for retry. Editing an alert during delivery does not remove the replacement. USDC monitoring remains independent and recurring. Old APY alerts recorded as already delivered are cleared on load; undelivered legacy targets acquire a direction from their first fresh rate. Polling detects observed crossings, not every intra-poll fluctuation.

## Loopscale borrow quotes (v1.7)

USDC views display the lowest fresh quoted APY with sufficient capacity for the configured minimum USDC, its term, maximum LTV, quote capacity and fetch time. The existing weighted-average loop APY remains separate. Alerts with a maximum borrow APY require an actual fresh qualifying quote; a tiny cheap offer cannot qualify the whole loop's liquidity. Capacity-only alerts continue when no rate is available. Availability and quotes are snapshots and may share underlying liquidity.

The adapter queries up to 100 lowest-rate offers per collateral per term, with four concurrent requests and six-second request deadlines. It does not aggregate offers or estimate a blended execution rate. It may miss qualifying offers beyond that sample or terms outside the four supported durations. Partial term failures are disclosed; failed quotes are not reused as current rates. Quote requests run inside the background source refresh, never in a Telegram button handler.

Contracts: [Get quotes](https://docs.loopscale.com/api-reference/data/markets/get-quotes), [Loop metrics](https://docs.loopscale.com/api-reference/guides/vault-and-loop-metrics). Rate conversion and duration enums were additionally verified against the official app bundle `https://app.loopscale.com/main.70b5674bfc2bf3bd.js`: `convertOrderbookQuote` uses `DM(raw) = raw / 1e6` as a fraction, so this app's percentage representation uses `raw / 1e4` (79500 → 7.95%). LTV likewise uses `raw / 1e4`; USDC amounts use `raw / 1e6`. Duration types are Days=0, Weeks=1, Months=2. The API page's APY-range description alone should not be used to infer the returned rate scaling.
