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

## Todo

### Access control (needs a second Identity Center user in `players`)

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| A1 | A `players` user can create, read and delete their own session | 202, 200, 200 with the permission set `gamevpn-player` only | both | safe |
| A2 | An Identity Center user outside `players` is refused | 403 from AWS | both | safe |
| A3 | A user cannot read or delete another user's session | 404 | both | safe |

### Session edges

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| S1 | A request nobody approves expires after 15 minutes | `expired`, message updated, node idles out | both | safe |
| S2 | DELETE while pending | `closed`, buttons replaced by the outcome, no peer ever added | both | safe |
| S4 | Session TTL | with `session_ttl` set to 5m: `expired`, peer removed, IP freed; then put it back | claude | reversible |
| S6 | Approval on a stopped node when the janitor starts it | `active` within the provisioning timeout from a cold start | claude | safe |

### Node failures

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| N3 | Egress cut (remove the security group egress rule) | existing tunnel keeps working, a new approval fails after 5 minutes, all recovers when the rule returns | both | reversible |
| N4 | Agent stopped (`systemctl stop vpn-agent`) | Discord alert "has not reported" after about 10 minutes, again after an hour; no alert after the agent is back | both | reversible |
| N5 | Agent that never stops the node (set `IDLE_STOP` huge in `/etc/gamevpn/env`, restart agent) | Discord alert "no live session" about 35 minutes after the last session | both | reversible |
| N6 | A fresh agent binary is promoted after its first healthy tick; a broken one is rolled back to `agent.good` | `cmp agent agent.good` after a normal boot; a deliberately broken build ends up on the old binary | both | reversible |
| N7 | DynamoDB policy narrowed (LeadingKeys) | agent reports nothing, the janitor sees silence, the start breaker stops restarts | both | destructive |
| N8 | Replace the instance with `-replace=aws_instance.vpn` while no session is live | cold cloud-init to `active` with no manual step: dnf with swap, wg0, nftables, agent; the server public key parameter updated | both | destructive |
| N9 | Start breaker: a node that never comes up | two starts without a heartbeat fail the live sessions and a 15 minute cooldown begins | both | destructive |

### Discord failures

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| C2 | The approval message was deleted by a person | closing the session still completes (404 is treated as synced) | both | safe |

### Cost and operations

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| O1 | AWS Budgets monthly alert exists and fires | a test budget with a tiny threshold sends a notice | you | safe |

### The game

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| G1 | Log in to the game through the tunnel from a real machine (import the config from `gamevpn-cli up` into the WireGuard app) | logs in, plays, latency acceptable | you | safe |
| G3 | Large packets pass (MTU) | `ping -M do -s 1372` through the tunnel works, or the config needs a lower MTU | both | safe |

Not covered until the client exists (Phase 4 onward): device flow sign-in, credential refresh during a session, tunnel set-up on Windows and macOS, route clean-up on disconnect.

## Findings from the checks

- **DNS is not sent through the tunnel.** The issued config has no DNS setting, so name lookups go to the client's local resolver (seen: 192.168.64.1 on eth0). Traffic is tunnelled; the queries are not.
- **The server's wg0 MTU is 8921**, derived by wg-quick from the interface MTU (9001), against a client default of 1420 and an Internet path of about 1500. A 20 MB download through the tunnel worked at about 96 Mbit/s with a client MTU of 1200, so nothing failed here, but a real 1500 byte path may fragment replies. Setting `MTU = 1420` on the server and clamping TCP MSS is the usual fix; decide before the real client ships.
- The running agent is `agent.good`: promotion after the first healthy tick happened (N6, first half).
- The max uptime timer is gone from the instance after the reboot.
