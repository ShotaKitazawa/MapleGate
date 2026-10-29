# End-to-end checks

Real AWS (us-west-2), real Discord, real WireGuard. The integration tests (`mise run test-integration`) stand in for most of this; what is listed here is what they cannot prove.

Status: **done** = observed in the real environment; **todo** = not yet. Who: *claude* runs it, *you* acts in Discord, the console or on a machine, *both* needs the two.
Risk: **safe** changes nothing lasting, **reversible** changes a setting and puts it back, **destructive** needs an explicit go-ahead each time.

## Done

| ID | What | Evidence |
|---|---|---|
| D1 | apply request, Discord post, approve, node starts from stopped, `active`, DELETE, `closed`, IP lease released, WORK reclaimed | `gamevpn-cli up/delete`, three sessions |
| D2 | Deny ends the session as `denied` and nothing is applied | deny pressed on purpose |
| D3 | Tunnel works: handshake, ping to 10.66.0.1, ping to 1.1.1.1 through NAT | container client |
| D4 | All traffic leaves through the node: `curl ifconfig.io` returns the node's public IP, TLS site returns 200 | the node's address instead of the client's own |
| D5 | A request without a valid signature, or with the old token, is rejected with 403 before the Lambda runs | curl |
| D6 | The Discord endpoint accepts Discord's signed PING and the button signatures | endpoint saved, buttons worked |
| D7 | An idle node powers itself off, and a later session starts it again | stopped 961 seconds (about 16 minutes) after the last session closed: 15 minutes of idle plus the agent tick and shutdown |
| D8 | The public IP changes across stop and start and GET returns the current one | two sessions, two endpoints |
| D9 | Posting the approval message works (bot token, channel id, permissions) and the message is updated with the outcome | `msg_synced` true after close |
| D10 | Pressing a button on a request whose session is already closed changes nothing | the press hit a session closed seconds earlier; no live session remained (catalogue D5 and D6) |
| D11 | Posting fails: a wrong `channel_id` gives 502, the session is `failed` with `discord unavailable`, and after restoring the value the next request is accepted (the slot was free) | recovery took about 10 minutes because the wrong value was cached again just before the restore |
| D12 | A second request while a session is live returns that session | 409 with the same `session_id` |
| D13 | The user name stored on a session is the Identity Center user name | META `user` = the first player |
| D14 | Log retention is set | 14 days on all three groups |
| D15 | N1: the agent killed with SIGKILL during a live session | systemd restarted it (new PID), the peer stayed, 81 pings with no loss, the session stayed `active` |
| D16 | N2: the instance rebooted during a live session | the tunnel dropped for about 21 seconds and came back on its own with the same key; GET stayed `active` because the reboot was shorter than the 2 minute heartbeat window |
| D17 | G2 (partly): with all traffic in the tunnel the visible address is the node's | IPv6 was `none` with and without the tunnel, but the test container has no IPv6 route, so a leak is not ruled out; see the findings below |
| D18 | S5: no handshake ends the session | `closed` with reason `idle` 642 seconds after the last recorded handshake (10 minutes plus the janitor interval) |
| D19 | The janitor raises no false node alert across a full cycle: session, idle, stop | no alert recorded, none in the log |
| D20 | A request awaiting approval does not start a stopped node | the node stayed `stopped` for 150 seconds with a pending request, over several janitor runs |
| D21 | S6: approval on a stopped node, the janitor starts it | `active` about 50 seconds after the request (limit 5 minutes) |
| D22 | S4: an approved session in use is extended and an unused one is not | with `session_ttl` 6m and `session_extend_every` 3m, the expiry moved every 3 minutes past the original 6; after the tunnel was dropped it kept extending until the handshake was 10 minutes old, then closed as `idle`; both values were put back |
| D23 | S1: a request nobody approves expires at the approval deadline | `expired`, reason `approval timeout`, `msg_synced` true; the Discord message text itself is not yet looked at |
| D24 | The tunnel cannot reach the node's own services or the VPC | IMDS token request got no answer (it got 200 before the fix), TCP 22 closed, ICMP to 10.66.0.1 and traffic to the internet still work; fixed by the `input` and `forward` rules in `node/nftables.conf` and `http_put_response_hop_limit = 1` |

## Todo

### Access control (needs a second Identity Center user in `players`)

A2 and A3 must be done before release.

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| A1 | A `players` user can create, read and delete their own session | 202, 200, 200 with the permission set `gamevpn-player` only | both | safe |
| A2 | An Identity Center user outside `players` is refused | 403 from AWS | both | safe |
| A3 | A user cannot read or delete another user's session | 404 | both | safe |

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
| O1 | The notifier posts to the approval channel | invoking `gamevpn-notify` with a test SNS event (done, the function returned 200) puts the test message in Discord | you | safe |
| O2 | A test budget with a tiny threshold reaches Discord (F3) | lower `monthly_budget_usd` to 0.01 for a moment, a notice appears, put it back | both | reversible |
| O3 | The janitor alarm fires and recovers | disable the schedule for 6 minutes: `gamevpn-janitor-silent` posts ALARM, then recovered after enabling it | both | reversible |
| O4 | After a session that was approved ends, its Discord message says how it ended | the message changes from approved to closed within about a minute of Disconnect | you | safe |

### The game

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|

## Client on macOS (Phase 4), by hand

`mise run build`, then `build/gamevpn up` in a terminal. Settings are in `~/Library/Application Support/gamevpn/config.json`.

| ID | What | Pass when | Who |
|---|---|---|---|
| M1 | First `up`: sign in, approval, tunnel | the browser opens once, Discord asks, `curl ifconfig.io` shows the node's address | both |
| M2 | A second `up` right after | no browser, no sign in prompt | you |
| M3 | Ctrl-C | the session is `closed`, `netstat -rn` shows no route of ours and no leftover `utun` address | you |
| M4 | `kill -9` of the privileged `gamevpn tunnel` and of `gamevpn up` | the next `up` starts clean; `/var/run/gamevpn-tunnel.json` is gone after a normal end | you |
| M5 | Wi-Fi switch during a session | traffic recovers within about 10 seconds, or ends with a message | you |
| M6 | `curl -6 ifconfig.io` | fails or shows the node's address, never the local one | you |
| M7 | A session left open by an earlier run | `up` closes it and asks again | you |
| M8 | A long run (several hours, sleep and wake, Wi-Fi changes, a container or VM bridge appearing) | no `Failed to send data packets` flood, the node's route follows the real default gateway only, the session stays `active` | you |

## Tray app and installer on macOS (Phase 5), by hand

Build with `client/packaging/macos/build-pkg.sh <settings file>`; install on a machine that has no client yet.

| ID | What | Pass when | Who |
|---|---|---|---|
| P1 | Install the unsigned pkg following `docs/friends-guide.md` | the guide's steps are enough; the dot appears; the settings file was placed | you |
| P2 | Connect from the menu | browser once, approval, green dot, a notice, the node's address from `curl ifconfig.io` | both |
| P3 | Quit the app while connected | the tunnel is taken down and the session closed before the dot disappears; `curl ifconfig.io` shows your own address | you |
| P3b | Open MapleGate from Spotlight after a Quit | the dot comes back; opening it while it runs starts no second one | you |
| P4 | Kill the app (`kill -9`) and leave it dead | the tunnel stays up for 15 minutes, then goes down; the node closes the session as `idle` | you |
| P5 | Disconnect from the menu | session `closed`, no route left, `curl ifconfig.io` shows your own address | you |
| P6 | The daemon refuses another user | a second macOS account (or `sudo -u`) gets "not allowed" from the socket | you |
| P7 | Uninstall script (done by the script from a terminal), and `gamevpn-uninstall.pkg` opened from Finder | nothing of ours is left in `/usr/local`, `/Library`, `/var/run`, no process, no receipt, no route | you |

## Full end-to-end, after the client exists (Phase 4 onward)

These need the real client and a real machine, so they wait until it is built. The server side is covered above.

| ID | What | Pass when | Who |
|---|---|---|---|
| F1 | Log in to the game through the tunnel and play | logs in, plays, latency acceptable | you |
| F2 | Path MTU with the config as issued | no stalls on large transfers; if there are, set the server `MTU` and clamp TCP MSS | both |
| F3 | AWS Budgets monthly alert exists and fires | a test budget with a tiny threshold sends a notice | you |
| F4 | Device flow sign-in, credential refresh during a session, tunnel set-up on Windows and macOS, route clean-up on disconnect | as the client phases define | both |
| F5 | Which game destinations need the tunnel (narrowing `AllowedIPs`), and whether server choice depends on where DNS is resolved | the list from a packet capture; DNS settings decided then | both |
