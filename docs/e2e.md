# End-to-end checks

Real AWS (us-west-2), real Discord, real WireGuard. Account, user and address values are left out; the figures that remain are measurements the settings were chosen from. The integration tests (`mise run test-integration`) stand in for most of this; what is listed here is what they cannot prove.

Status: **done** = observed in the real environment; **todo** = not yet. Who: *claude* runs it, *you* acts in Discord, the console or on a machine, *both* needs the two.
Risk: **safe** changes nothing lasting, **reversible** changes a setting and puts it back, **destructive** needs an explicit go-ahead each time.

## Done

| ID | What | Evidence |
|---|---|---|
| D1 | apply request, Discord post, approve, node starts from stopped, `active`, DELETE, `closed`, IP lease released, WORK reclaimed | `maplegate-cli up/delete`, three sessions |
| D2 | Deny ends the session as `denied` and nothing is applied | deny pressed on purpose |
| D3 | Tunnel works: handshake, ping to 10.66.0.1, ping to 1.1.1.1 through NAT | container client |
| D4 | All traffic leaves through the node: `curl ifconfig.io` returns the node's public IP, TLS site returns 200 | the node's address instead of the client's own |
| D5 | A request without a valid signature, or with the old token, is rejected with 403 before the Lambda runs | curl |
| D6 | The Discord endpoint accepts Discord's signed PING and the button signatures | endpoint saved, buttons worked |
| D7 | An idle node powers itself off, and a later session starts it again | stopped 961 seconds (about 16 minutes) after the last session closed: 15 minutes of idle plus the agent tick and shutdown |
| D8 | The public IP changes across stop and start and GET returns the current one | two sessions, two endpoints |
| D9 | Posting the approval message works (bot token, channel id, permissions) and the message is updated with the outcome | the first message of a session turned from approved to closed after Disconnect (D25); an earlier reading of `msg_synced` true at approval time was not evidence |
| D10 | Pressing a button on a request whose session is already closed changes nothing | the press hit a session closed seconds earlier; no live session remained (catalogue D5 and D6) |
| D11 | Posting fails: a wrong `channel_id` gives 502, the session is `failed` with `discord unavailable`, and after restoring the value the next request is accepted (the slot was free) | recovery took about 10 minutes because the wrong value was cached again just before the restore |
| D12 | A second request while a session is live returns that session | 409 with the same `session_id` |
| D13 | The user name stored on a session is the Identity Center user name | META `user` = the first player's Identity Center user name |
| D14 | Log retention is set | 14 days on all three groups |
| D15 | N1: the agent killed with SIGKILL during a live session | systemd restarted it (new PID), the peer stayed, 81 pings with no loss, the session stayed `active` |
| D16 | N2: the instance rebooted during a live session | the tunnel dropped for about 21 seconds and came back on its own with the same key; GET stayed `active` because the reboot was shorter than the 2 minute heartbeat window |
| D17 | G2 (partly): with all traffic in the tunnel the visible address is the node's | IPv6 was `none` with and without the tunnel, but the test container has no IPv6 route, so a leak is not ruled out ; M6 repeats it on a real machine |
| D18 | S5: no handshake ends the session | `closed` with reason `idle` 642 seconds after the last recorded handshake (10 minutes plus the janitor interval) |
| D19 | The janitor raises no false node alert across a full cycle: session, idle, stop | no alert recorded, none in the log |
| D20 | A request awaiting approval does not start a stopped node | the node stayed `stopped` for 150 seconds with a pending request, over several janitor runs |
| D21 | S6: approval on a stopped node, the janitor starts it | `active` about 50 seconds after the request (limit 5 minutes) |
| D22 | S4: an approved session in use is extended and an unused one is not | with `session_ttl` 6m and `session_extend_every` 3m, the expiry moved every 3 minutes past the original 6; after the tunnel was dropped it kept extending until the handshake was 10 minutes old, then closed as `idle`; both values were put back |
| D23 | S1: a request nobody approves expires at the approval deadline | `expired`, reason `approval timeout`, `msg_synced` true; the Discord message text itself is not yet looked at |
| D24 | The tunnel cannot reach the node's own services or the VPC | IMDS token request got no answer (it got 200 before the fix), TCP 22 closed, ICMP to 10.66.0.1 and traffic to the internet still work; fixed by the `input` and `forward` rules in `node/nftables.conf` and `http_put_response_hop_limit = 1` |
| D25 | O4: after a session that was approved ends, its Discord message says how it ended | the message changed from approved to closed after Disconnect from the menu |
| D26 | P4: `kill -9` of the app | the tunnel stayed up, launchd started the app again, and it showed Connected with the original start time; the diagnostics were taken from it |
| D27 | P8: Copy diagnostics | version, macOS, tunnel state, routes (the node on the real gateway, halves into `utun8`, the VM bridge ignored), daemon log; no token or key. Other devices' MAC addresses were in the routes and are now dropped |
| D28 | C1: uninstalling with the tunnel up | the tunnel was gone afterwards and nothing of ours was left |
| D29 | F7, first measurement (2026-10-29), one author's line: a Mac in Japan, a 100 MB object in S3 us-west-2. The figures are what the per-peer limit of about 10 Mbps (O6, `node/nftables.conf`) was set against; they depend on the line and the time of day and are not a guarantee | with the tunnel: one stream 2.8 and 4.4 MB/s, four streams 5.6 MB/s (45 Mbps); without it: one stream 3.9 and 5.2 MB/s, four streams 12.3 MB/s (99 Mbps). Round trip to the node 121 ms, and 5 ms more to a host beyond it. The node's CPU stayed under 3% and its credits did not fall (balance 43 and rising, no surplus), so the CPU is not the limit. Published figures: burst up to 5 Gbps (AWS); baseline 0.032 Gbps (third party only, unconfirmed) |
| D30 | A1 and A2: who may call the API | a `players` user creates, reads and deletes their own session (D1 and every tray run). Outside `players`: no credentials (`GetRoleCredentials` is `ForbiddenException: No access` for a second Identity Center user after leaving the group); an unsigned call gets 403 from AWS; the admin IAM user gets 403 `forbidden` from the handler; and a player cannot read DynamoDB directly (`AccessDeniedException`) |
| D31 | A3: a user cannot read or delete another user's session | with a second user (in `players`), GET and DELETE of the first player's pending session both gave 404 and the session stayed `pending`; the first player read it fine |
| D32 | F1 and F2: the game through the tunnel | the game logged in and played well enough through the full tunnel (F1); the 25 MB and 100 MB downloads of the speed test ran through the tunnel at the issued MTU without stalling (F2) |
| D33 | By hand on macOS, with the first builds: M1 (first `up` signs in, is approved, brings the tunnel up), M2 (a second `up` opens no browser), P2 (Connect from the menu; `curl ifconfig.io` shows the node), P3 (Quit disconnects), P5 (Disconnect leaves no route or interface), P7 (the uninstall script left nothing); later builds repeat them | each as its row said |
| D34 | F5 on macOS (2026-10-30, while playing): the game's process and its bandwidth | The game is one native process that exists only while it is played; the launcher and the Wine helpers outlive it, so only the game's own name goes in the settings file (kept outside this repository). Per-connection counters of `nettop`, summing only the increases, in two maps: 2 minutes, download 8 kbit/s on average and 114 kbit/s at most over 2 seconds, upload 1 and 5 kbit/s; 30 seconds in another map, download 108 and 134 kbit/s, upload 2 and 4 kbit/s. The largest reading is about 1/70 of the per-peer limit of about 10 Mbps. The per-process row of `nettop` is not usable: in the 2 minute run it summed to 22 MB with a 13.5 Mbit/s peak because it drops by about 2.8 MB and returns when a connection comes and goes, while the connections themselves moved 0.12 MB. Login and a crowded place are not shown yet |

## Todo

### Session edges

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| S2 | DELETE while pending | `closed`, buttons replaced by the outcome, no peer ever added | both | safe |

### Node failures

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| N4 | Agent stopped (`systemctl stop vpn-agent`) | Discord alert "has not reported" after about 10 minutes, again after an hour; no alert after the agent is back | both | reversible |
| N5 | Agent that never stops the node (set `IDLE_STOP` huge in `/etc/gamevpn/env`, restart agent) | Discord alert "no live session" about 35 minutes after the last session | both | reversible |
| N6 | A fresh agent binary is promoted after its first healthy tick; a broken one is rolled back to `agent.good` | `cmp agent agent.good` after a normal boot; a deliberately broken build ends up on the old binary | both | reversible |


### Cost and operations

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| O2 | A test budget with a tiny threshold sends its mail (F3) | lower `monthly_budget_usd` to 0.01 for a moment, a mail arrives, put it back | both | reversible. Lowered at 2026-10-29 20:41 JST; **put it back to 10 when the mail has arrived** |
| O5 | Port 25 from the tunnel is refused, so the node cannot send spam | after the node picks up the new setup: `nc -vz -w5 <any mail server> 25` through the tunnel is reset at once, and port 587 still connects | both | needs the setup delivered and the node restarted |
| O6 | One peer is held to about 10 Mbps in each direction | through the tunnel, a 100 MB download from S3 takes about 80 seconds or more (about 1.2 MB/s), the game and ping are unaffected, and a second peer at the same time is not slowed by the first | both | needs the setup delivered and the node restarted; the rate and burst are in `node/nftables.conf` |

## Client on macOS, from a terminal, by hand

`mise run build`, then `build/maplegate up` in a terminal. Settings are in `~/Library/Application Support/MapleGate/config.json`.

| ID | What | Pass when | Who |
|---|---|---|---|
| M3 | Ctrl-C | the session is `closed`, `netstat -rn` shows no route of ours and no leftover `utun` address | you |
| M4 | `kill -9` of the privileged `maplegate tunnel` and of `maplegate up` | the next `up` starts clean; `/var/run/maplegate-tunnel.json` is gone after a normal end | you |
| M5 | Wi-Fi switch during a session | traffic recovers within about 10 seconds, or ends with a message | you |
| M6 | `curl -6 ifconfig.io` | fails or shows the node's address, never the local one | you |
| M7 | A session left open by an earlier run | `up` closes it and asks again | you |
| M8 | A long run (several hours, sleep and wake, Wi-Fi changes, a container or VM bridge appearing) | no `Failed to send data packets` flood, the node's route follows the real default gateway only, the session stays `active` | you |

## Tray app and installer on macOS, by hand

Build with `client/packaging/macos/build-pkg.sh <settings file>`; install on a machine that has no client yet.

| ID | What | Pass when | Who |
|---|---|---|---|
| P1 | Install from a copy downloaded through a link, as a friend does, following `docs/friends-guide.md` | the guide's Gatekeeper steps are enough. The locally built pkg opened from Finder installed without any (it carries no quarantine flag), so that step has not been exercised | you |
| P3b | Open MapleGate from Spotlight after a Quit | the dot comes back; opening it while it runs starts no second one | you |
| P9 | Look at the menu bar icon in light and dark mode in each state, and at the app in Spotlight and Finder | the menu bar shows a dot: grey idle, amber waiting, green connected, red failed; the app icon is a red maple leaf, tilted, with no square behind it | you |
| P4b | Stop the app for good (`launchctl bootout gui/$(id -u)/com.maplegate.tray`, then `kill -9`) | the tunnel goes down after 15 minutes; the node closes the session as `idle` | you |
| P6 | The daemon refuses another user | a second macOS account (or `sudo -u`) gets "not allowed" from the socket | you |

## Full end-to-end

These need the real client on a real machine. The server side is covered above.

| ID | What | Pass when | Who |
|---|---|---|---|
| F4 | Device flow sign-in, credential refresh during a session, tunnel set-up on Windows and macOS, route clean-up on disconnect | as the client phases define | both |
| F5 | What the game process is called on Windows, and a longer bandwidth reading | macOS is in D34. Still open: the process name on Windows (for F8), and a reading of 10 minutes or more that includes map changes, login and a crowded place (assumed 1 to 2 Mbps). Narrowing `AllowedIPs` is not planned: the game's servers are spread over AWS and CDNs, a country exclusion removes nothing, and an AWS-ranges list would break the game as soon as one range is missing. **Required before release** | both |
| F7 | Speed: the theoretical value and the measured one | the ceiling is written down for each part with its source (the node's network baseline and burst for `t4g.nano` from AWS's published figures, the person's own line, WireGuard's overhead at the tunnel MTU), then measured with `iperf3` in both directions, with and without the tunnel, once while the node has burst to spend and once after it is spent, plus the round trip time added (`ping` with and without). Pass when each measurement lies within the ceiling and every gap is explained. **Required before release. Still open after D29: a transfer long enough to spend the burst, the node's own bandwidth counters (`ethtool -S`, `bw_out_allowance_exceeded`), and the AWS-published baseline** | both |
| F8 | The client disconnects when the game is not running | on each of macOS and Windows: with the game running nothing appears; 10 minutes after the game has closed a dialog asks to disconnect; with no answer for 2 minutes it disconnects; starting the game before then cancels it. **Required before release, by hand on each OS** | you |
