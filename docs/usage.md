# Using MapleGate

## The client (macOS)

`mise run build` produces `build/maplegate`. Put the deployment's settings (none of them is secret) in `~/Library/Application Support/MapleGate/config.json`:

```
{"start_url": "<Identity Center start URL>", "region": "us-west-2", "account_id": "<account>", "role_name": "gamevpn-player", "api_url": "<session_api_url output>", "game_processes": ["<program name of the game>"]}
```

`build/maplegate up` asks for the administrator password once (the tunnel needs root), signs in in the browser the first time only, asks for a session, waits for the Discord approval (unless the deployment turned it off), and brings up the tunnel. Ctrl-C closes the session and removes the routes. `maplegate up` is the terminal client; `maplegate login` only signs in. All traffic goes through the node, IPv6 included (the node drops it). Windows is not implemented yet.

## The installer (macOS)

`client/packaging/macos/build-pkg.sh <settings file>` builds `build/MapleGate.pkg` (the tray app, the daemon, `MapleGate.app` to open it from Spotlight, log rotation) and `build/MapleGate-uninstall.pkg`. The bot's reply to the download command carries the points where people get stuck (the unsigned installer, the first password); the guides below are the longer version. The settings file is the JSON above; it is copied into the package, so a friend never writes it. What a friend does is in [docs/friends-guide.md](friends-guide.md). The pkg is not signed, so the guide says how to get past Gatekeeper.

## Debugging with the CLI (no tunnel)

```
aws sso login --profile <your profile>
export AWS_PROFILE=<your profile> MAPLEGATE_API_URL=<session_api_url output>
go run ./client/cmd/maplegate-cli up > wg0.conf
```
`up` creates a session and waits; press Approve in Discord (unless the deployment turned approval off). It then prints a WireGuard config to stdout (the private key stays on your machine). `get <id>` and `delete <id>` read and end a session. An unsigned `curl` to the URL must return 403.

Clients must treat only `denied`, `failed`, `closed` and `expired` as the end of a session. `provisioning` after `active`, or an HTTP 503, means the node or the table is briefly unreachable and the tunnel should stay up.

The node has no SSH and no inbound port besides WireGuard. For the first boots, use Session Manager (`aws ssm start-session --target <instance id>`) or `aws ec2 get-console-output` for the cloud-init log; once the node is proven, drop the permission with `-var node_session_manager=false`.

The live checks still open are the Todo list in [docs/e2e.md](e2e.md).
