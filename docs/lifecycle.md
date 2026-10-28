# VPN node lifecycle

DynamoDB is the single source of truth. Every component converges on it; none holds authoritative state of its own.

## Invariants

- I1 State lives only in DynamoDB. The kernel peer table and the Discord message are derived from it.
- I2 Every attribute has one writer. Lambdas write only through conditional transitions.
- I3 Every transient state has a deadline and a janitor that reclaims it.
- I4 Side effects (StartInstances, Discord update, IP release, peer apply) are idempotent and level-triggered.
- I5 Fail-static: when DynamoDB is unreadable the agent keeps peers and degrades by local expiry and local handshake idleness.
- I6 An IP lease is released only after the node confirmed the peer's removal, or the node is confirmed stopped.
- I7 Absence is never a reason to remove a peer. Removal needs evidence (terminal or expired item) or an unknown peer.
- I8 Cost and exposure have absolute caps (MAX_UPTIME, start breaker, local expiry).

## Data model (table `gamevpn`)

| PK / SK | Writer | Content |
|---|---|---|
| `SESSION#<sid>` / `META` | Lambda | user, public_key, status, address, approver, discord_message_id, msg_synced, created_at, approved_at, expires_at, close_reason, error, ttl |
| `WORK` / `<sid>` | intent: Lambda, observation: node | intent: phase (mirror of META.status), public_key, address, expires_at. observation: applied_at, applied_boot_id, last_handshake_at, removed_at, error |
| `NODE` / `vpn` | node | instance_id, boot_id, public_ip, last_seen, state, mode |
| `HEALTH` / `janitor` | janitor | last_run, start_failures, cooldown_until |
| `USER#<u>` / `ACTIVE`, `RATELIMIT`; `IP#<a>` / `LEASE` | Lambda | as before |

A META transition and its WORK mirror are always one transaction. The janitor deletes a WORK item after `removed_at`.

The API status is derived: `approved` with `WORK.applied_boot_id == NODE.boot_id` and `NODE.last_seen` within STALE is `active`; any other `approved` is `provisioning`. The endpoint is `NODE.public_ip`.

## META.status transitions

- pending -> approved (approval; IP leased; expires_at = now + TTL)
- pending -> denied | expired (janitor, approval deadline) | failed
- approved -> closed (DELETE, or janitor on handshake idleness) | expired (janitor) | failed (janitor: not applied within 5 min, or node unhealthy)
- A terminal transition also frees `USER#ACTIVE`; it never frees the IP lease (I6). Deleting one's own pending session also frees `RATELIMIT`.

## Components

- session-api: POST creates META + WORK + ACTIVE + RATELIMIT in one transaction; 409 carries the existing session_id; StartInstances is best effort. GET and DELETE touch DynamoDB only.
- discord-interactions: immediate ack, then async self-invoke. Approval = lease IP, conditional transition (release the lease if it lost), StartInstances. Signed timestamps older than 5 min are rejected.
- janitor (Scheduler, 1 min, concurrency 1): deadlines and idleness transitions; start the node when live sessions exist and it is stopped (breaker-guarded); release IPs and delete WORK after `removed_at`; set `removed_at` itself only while the node is stopped; retry unsynced Discord messages; write HEALTH.
- vpn-agent (node, tick 10 s, heartbeat 30 s): converge peers on WORK (validated, full diff), report observations, decide to stop. MAX_UPTIME is a separate timer.

## Agent modes

- normal: as above.
- blind (3 consecutive failed ticks): no new peers; keep existing ones; remove by local expiry and local handshake idleness (10 min); power off after 30 min with zero peers. The first successful read re-converges.
- Errors: transient (timeout, 5xx, throttle) back off with jitter; auth errors refresh IMDS credentials once, then blind. Per-call timeout 5 s, per-tick 8 s, systemd watchdog.

## Parameters

AGENT_TICK 10s, HEARTBEAT 30s, STALE 120s, BOOT_GRACE 10m, IDLE_STOP 15m, BLIND_PEER0_STOP 30m, MAX_UPTIME 5h, BLIND_MAX unlimited, START_BREAKER 2 consecutive / cooldown 15m, PARAMS_CACHE_TTL 5m, DISCORD_TS_TOLERANCE 5m, MSG_SYNC_GIVEUP 1h, CLOCK_MARGIN 30s.

## Failure catalogue

Each row is a test (see `lambda/internal/scenario`, `node/agent`).

### A. VM <-> DynamoDB
- A1 total outage: blind; new approvals unapplied and failed by the janitor after 5 min; existing sessions keep playing.
- A2 throttling: transient, back off.
- A3 auth failure (IAM, role, IMDS, token): refresh once, then blind; heartbeat stops so the janitor sees an unhealthy node.
- A4 clock skew breaks SigV4: as A3.
- A5 hang or very slow call: call timeout and watchdog.
- A6 stale read omits an item: consistent reads, and I7.
- A7 malformed WORK item: validated per item, ignored with `error`.
- A8 WORK looks empty (wrong table or region): I7, local expiry.
- A9 reads work but heartbeat writes fail: local apply continues; GET shows `provisioning`; clients must not disconnect on it.

### B. The VM
- B1 agent crash or OOM: systemd restarts; peers survive in the kernel; resume from the local cache.
- B2 agent panic loop: backoff; MAX_UPTIME; the breaker stops restarts.
- B3 OS reboot drops peers: agent re-applies from WORK; GET is `provisioning` meanwhile.
- B4 stop/start changes the public IP: agent updates NODE.public_ip.
- B5 wg0 cannot be created: agent reports `error`; janitor fails the session after 5 min; breaker.
- B6 setup failure (package repo down): setup is an idempotent per-boot oneshot; agent does not start; breaker.
- B7 artifact fetch fails: previous cached binary; no cache means breaker.
- B8 bad agent release: automatic rollback to `agent.good`.
- B9 disk full or read-only: skip the cache write; journald size cap.
- B10 clock step: monotonic clock for intervals, wall clock with CLOCK_MARGIN for expiry.
- B11 server key lost or instance replaced: public key parameter refreshed each boot; the janitor fails live sessions when the instance is terminated.
- B12 two nodes: instance_id check on NODE; takeover only when last_seen is older than 5 min.

### C. Lambda <-> AWS
- C1 DynamoDB error: 503; transactions leave no half state.
- C2 transaction commit with lost response: retry gets 409 + session_id; transitions are idempotent.
- C3 Lambda crash mid-flow: leaked lease expires by TTL; the janitor starts the node and resyncs Discord.
- C4 StartInstances fails (capacity, quota, throttle): non-fatal; janitor retries; repeated failure fails the session. Single-AZ capacity loss cannot be avoided.
- C5 request while the node is stopping: janitor starts it within a minute after it stopped.
- C6 stop vs create race: same as C5; the agent re-queries right before power off.
- C7 instance terminated: janitor fails live sessions and notifies; it does not start anything.
- C8 EC2 API throttle: backoff on non-critical paths.
- C9 Parameter Store outage: stale cache continues.
- C10 secret rotation: 5 min TTL and invalidation on Discord 401.
- C11 reserved concurrency exhausted: 429 and retry; Discord re-press is idempotent.
- C12 duplicate async delivery: conditional transition.
- C13 janitor duplicate or skipped: concurrency 1, idempotent, next minute catches up.
- C14 janitor stopped: the node enforces expiry and stop locally; GET/POST evaluate expiry lazily.
- C15 start loop: two starts without heartbeat fail live sessions and pause starts for 15 min.
- C16 META removed by TTL, orphan WORK: treated as terminal, deleted after `removed_at`.
- C17 regional outage: out of scope; MAX_UPTIME and budget alarms bound exposure.

### D. Discord
- D1 post fails: session failed, 502, active slot and rate limit released.
- D2 update fails: janitor retries; 404 means give up; stop after 1 h.
- D3 bot token revoked: everything fails; alarm.
- D4 bad signature or replay: verification and timestamp window; per-sid idempotency.
- D5 button on a terminal session: message refreshed to the current outcome.
- D6 double or concurrent press: one conditional transition wins.
- D7 Discord outage: pending expires; user retries.

### E. Client contract
- E1 never tear down an established tunnel on API errors or non-terminal statuses; disconnect only on a terminal status.
- E2 lost POST response: retry returns 409 + session_id; `GET /session` also works.
- E3 client crash loses the in-memory key: `GET /session`, `DELETE`, request again.
- E4 endpoint change after node restart: poll GET until `active`, reconfigure with the same key and address.
- E5 client clock skew breaks SigV4: surface a clear error.

### F. Operations
- F1 instance replaced by apply: `ami` and `user_data` ignored; changes ship via S3 on the next boot; replace explicitly.
- F2 table deletion: deletion protection.
- F3 bad Lambda deploy: idempotent transitions plus janitor; redeploy to roll back.
- F4 IAM mistakes: seen as A3; covered by the live checklist.
- F5 runaway cost: MAX_UPTIME, breaker, budgets.
- F6 secret leakage: never log keys, tokens or bodies.

## Who may do what to the instance

Terraform creates and deletes the instance. The Lambdas may only start it (`ec2:StartInstances` on this one instance) and describe it. Only the node stops itself, through the agent or the 5 hour cap: it can re-check DynamoDB just before powering off, and it can still stop when DynamoDB is unreachable.

## Known compromises

The instance is long-lived and `ignore_changes` covers `ami`, `user_data` and `associate_public_ip_address`. A replacement regenerates the server key, which lives only on the instance, and drops every live session. The costs:

- The AMI never updates, so OS and kernel patches stop.
- `ignore_changes` also hides unintended changes to those attributes.
- A persistent `InsufficientInstanceCapacity` in the single AZ can only be fixed by a replacement.

Revisit when the server key moves off the instance (an SSM SecureString, for example). Replacement then costs only the live sessions, and a procedure that replaces the node while no session is live, with a fresh AMI, lets `ami` come out of `ignore_changes`. `associate_public_ip_address` is there because a stopped instance reports no public IP and the provider reads that as drift; drop it once the provider or the layout no longer does.
