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
| D7 | An idle node powers itself off, and a later session starts it again | seen once; the time to stop was not measured |
| D8 | The public IP changes across stop and start and GET returns the current one | two sessions, two endpoints |
| D9 | Posting the approval message works (bot token, channel id, permissions) and the message is updated with the outcome | `msg_synced` true after close |

## Todo

### Access control (needs a second Identity Center user in `players`)

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| A1 | A `players` user can create, read and delete their own session | 202, 200, 200 with the permission set `gamevpn-player` only | both | safe |
| A2 | An Identity Center user outside `players` is refused | 403 from AWS | both | safe |
| A3 | A user cannot read or delete another user's session | 404 | both | safe |
| A4 | The user name stored on a session is the Identity Center user name | META `user` equals the name | claude | safe |
| A5 | A second request while one session is live returns the existing session | 409 with that `session_id` | claude | safe |

### Session edges

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| S1 | A request nobody approves expires after 15 minutes | `expired`, message updated, node idles out | both | safe |
| S2 | DELETE while pending | `closed`, buttons replaced by the outcome, no peer ever added | both | safe |
| S3 | Pressing Approve or Deny on an already decided request | message refreshed, nothing changes (D5, D6 of the catalogue) | both | safe |
| S4 | Session TTL | with `session_ttl` set to 5m: `expired`, peer removed, IP freed; then put it back | claude | reversible |
| S5 | No handshake for 10 minutes ends the session | connect, go silent: `closed` with reason `idle` | both | safe |
| S6 | Approval on a stopped node when the janitor starts it | `active` within the provisioning timeout from a cold start | claude | safe |

### Node failures

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| N1 | Agent killed with SIGKILL during a live session | systemd restarts it, the tunnel never drops, the session stays `active` | both | safe |
| N2 | Instance rebooted during a live session | GET shows `provisioning`, then `active`; the client reconnects with the same key | both | safe |
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
| C1 | Posting fails (set `channel_id` to a wrong value for a few minutes) | POST returns 502, the session is `failed`, the slot is free; restore and it works | claude | reversible |
| C2 | The approval message was deleted by a person | closing the session still completes (404 is treated as synced) | both | safe |

### Cost and operations

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| O1 | AWS Budgets monthly alert exists and fires | a test budget with a tiny threshold sends a notice | you | safe |
| O2 | CloudWatch log retention is set | retention shown on the three log groups | claude | safe |

### The game

| ID | What | Pass when | Who | Risk |
|---|---|---|---|---|
| G1 | Log in to the game through the tunnel from a real machine (import the config from `gamevpn-cli up` into the WireGuard app) | logs in, plays, latency acceptable | you | safe |
| G2 | No IPv6 or DNS leak with the config as issued | `curl -6 ifconfig.io` fails or exits through the node; DNS queries go through the tunnel | both | safe |
| G3 | Large packets pass (MTU) | `ping -M do -s 1372` through the tunnel works, or the config needs a lower MTU | both | safe |

Not covered until the client exists (Phase 4 onward): device flow sign-in, credential refresh during a session, tunnel set-up on Windows and macOS, route clean-up on disconnect.
