## v1.7 — 2026-10-03

- `go test ./...`: passed (32 top-level tests).
- `go vet ./...`: passed.
- Windows executable build: passed (`bin/onyc-watch-v1.7.exe`).
- Live read-only adapter verification completed at approximately 06:08 UTC, using the existing Windows proxy. No Telegram token, account messages or blockchain transactions were used.
- ONyc / USDC: 9 quotes; lowest sufficient quote for 100 USDC was 7.95%, 1 day, max LTV 70%.
- srONyc / USDC: 6 quotes; lowest sufficient quote for 100 USDC was 6.00%, 1 day, max LTV 80%.
- PT-ONyc-10JAN27 / USDC: 19 quotes; lowest sufficient quote for 100 USDC was 7.99%, 1 month, max LTV 70%.
- These are historical verification observations, not current rates.
- Regression coverage includes raw rate/amount conversion, request terms, malformed/empty quote responses, retained loop capacity on quote failure, insufficient cheap liquidity, stale quotes and rate-filter behavior.
- The launcher uses v1.7 after restart; the existing live Telegram process was not stopped.

## v1.6 — 2026-10-03

- `go test ./...`: passed (29 top-level tests).
- `go vet ./...`: passed.
- Windows executable build: passed (`bin/onyc-watch-v1.6.exe`).
- Added regression coverage for lower/higher/equal targets, crossing overshoot, direction persistence, one-time target/change delivery, failed delivery retention, replacement protection, post-delivery price reversal and deletion surviving restart.
- Updated market-selection regression: a 12.9% target set at 18.3% waits for a fall instead of immediately firing.
- No live Telegram messages or process restart performed during verification.

## v1.5 — 2026-10-03

- `go test ./...`: passed (27 top-level tests).
- `go vet ./...`: passed.
- Windows executable build: passed (`bin/onyc-watch-v1.5.exe`).
- Updated flow coverage: single Settings entry, configured-user management panel, Edit path, first-time market picker, and retained enable/disable behavior. Existing callback payloads remain supported for older Telegram messages.
- No live process restart or live Telegram messages were performed.

## v1.4 — 2026-10-03

- `go test ./...`: passed (27 top-level tests).
- `go vet ./...`: passed.
- Windows executable build: passed (`bin/onyc-watch-v1.4.exe`).
- Regression coverage: market selection and numeric confirmation; only the chosen APY alerts; cancellation preserves prior target; selected market persists; APY edits preserve USDC history and in-flight receipts; new users start unsubscribed; legacy APY configurations require selection.
- Existing tests now explicitly subscribe to their fixture market, matching the new subscription model.
- No live Telegram messages were sent during verification. Restart the existing process and select the desired APY market in Settings.

## v1.3 — 2026-10-03

- `go test ./...`: passed (24 top-level tests).
- `go vet ./...`: passed.
- Windows executable build: passed (`bin/onyc-watch-v1.3.exe`).
- Tests cover independent APY/USDC disabling, persisted settings, legacy enabled defaults, re-enabling, idempotent disable callbacks, visible status, manual APY access and global pause.
- Existing live process was not restarted; restart using Start-Bot.cmd to apply this version.

## v1.2 — 2026-10-03

- `go test ./...`: passed (22 top-level tests).
- `go vet ./...`: passed.
- Added a local Telegram simulation that holds both alert delivery and callback acknowledgement open; the APY response completes while both remain blocked.
- Added delivery-state tests for success, changed preferences, pause, deletion, changed market condition and Telegram retry-after.
- Windows executable: `bin/onyc-watch-v1.2.exe`.
- No live Telegram latency benchmark was performed. Restart the running bot to use this build. End-to-end delay still depends on Telegram and the network/proxy path.
- Race-detector testing is unavailable with the installed windows/386 Go target; shared user state is accessed only by the event loop.

# Verification

## v1.1 — 2026-10-03

- 20 tests passed; go vet passed; Windows v1.1 executable rebuilt.
- New tests verify independent provider updates, retained stale timestamps, zero provider requests from cached commands, and an APY view without repeated links that fits one message.
- Current APY, USDC Availability and Refresh return cached data without waiting for external providers.
- The running v1.0 process was not interrupted. Restart with Start-Bot.cmd to activate v1.1. Existing credentials and user state were not modified.
- Live Telegram timing was not measured in this update. Network/proxy latency and outbound alert delivery can still affect response times.

## Initial build — historical verification

Verified on 2026-10-02 on Windows with Go 1.26.3 (windows/386).

- `go test -v -count=1 ./...`: **16 tests passed**.
- `go vet ./...`: **passed**.
- Windows executable build: **passed**.
- `Start-Bot.ps1 -Check`: **passed**, using the user's existing Windows proxy.
- `Start-Bot.ps1 -Preview`: **passed**, using the actual built executable.
- Exponent yield and tranche adapters: **live response verified**, covering YT ONyc, YT srONyc, srONyc and jrONyc.
- Kamino: **live response verified**, with separately identified fixed-term and variable-rate USDC reserves.
- Loopscale: **live response verified**, covering current ONyc-related USDC loops, filtering expired recognized PT slugs.
- Telegram callback/settings flow: **tested against a local mock Telegram server**.

Live Telegram login and delivery have **not** been tested: no BotFather token was supplied. No bot has been deployed or left running. Docker files are supplied but Docker deployment has not been run.

Loopscale current borrow APY is not available in the integrated loop-info response. Capacity is reported liquidity, not an executable quote. This limitation is displayed in the bot and documented in README.md.

The captured preview and live JSON are historical verification artifacts, not current investment data.
