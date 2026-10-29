# MapleGate

An on-demand WireGuard gateway on AWS, gated by IAM Identity Center sign-in and a Discord approval button. A small node starts when someone is approved and stops itself when nobody is using it, so an idle deployment costs close to nothing. It was written to let a few friends reach a game's servers from one region.

This is a reference implementation for AWS, not a portable product: it uses DynamoDB, Lambda Function URLs with IAM auth, IAM Identity Center, EventBridge Scheduler, EC2, S3 and SSM Parameter Store directly, and nothing abstracts them. Names and regions are variables (see `infra/main/variables.tf`); a provider other than AWS would be a rewrite of the Lambdas and the client's sign-in.

How the node, the Lambdas and DynamoDB stay consistent (and what happens when each of them breaks) is specified in [docs/lifecycle.md](docs/lifecycle.md). The client plan is in [docs/client.md](docs/client.md); what has been checked against the real environment, and what has not, is in [docs/e2e.md](docs/e2e.md).

## Architecture

DynamoDB is the single source of truth; nothing is ever pushed to the node.

- `session` Lambda (Function URL): creates, reads and closes sessions. It only writes intent to DynamoDB.
- `discord` Lambda (Function URL): verifies Discord's signature and records approve / deny.
- `janitor` Lambda (EventBridge Scheduler, every minute): enforces deadlines, starts the node when sessions are waiting, frees IPs, and keeps Discord messages in sync.
- `vpn-agent` (on the EC2 node): converges WireGuard peers on the work queue in DynamoDB, reports what it applied, and powers the node off when nothing is live.
- A tunnel user can reach the internet through the node and nothing else: `node/nftables.conf` drops private and link-local destinations (the instance metadata service and the VPC among them) and everything addressed to the node except ping, and the metadata hop limit is 1. It also refuses port 25 (no spam from the node's address) and holds each peer to about 10 Mbps in each direction, so one friend's download does not take the node from the others.
- Node scripts and the agent binary ship through the artifacts bucket and are fetched on every boot, so changing them never replaces the instance (which would regenerate the server key).

## Prerequisites (Phase 0, manual)

### AWS

Administration (Terraform, the console) uses an IAM user in the `admin` group, which has only `AdministratorAccess`, signed in with `aws login`. Friends are Identity Center users and never get more than the `gamevpn-player` permission set. Identity Center's MFA setting covers everyone in the instance and is changed only in the console (Settings, Authentication); it is switched off here because the Discord approval is the second factor.

Run `terraform plan` and read it before every `apply`.

1. Use a Paid-plan account. Free-plan accounts may not be able to enable Organizations / Identity Center; upgrade first if they cannot.
2. Enable AWS Organizations, then IAM Identity Center (organization instance).
3. Create the state bucket once with local state:
   ```
   terraform -chdir=infra/bootstrap init && terraform -chdir=infra/bootstrap apply
   ```
4. Build the binaries Terraform uploads and zips, then apply:
   ```
   mise run build
   terraform -chdir=infra/main init && terraform -chdir=infra/main apply
   ```
   Reserved concurrency (session 2 / discord 3 / janitor 1) fails on accounts whose Lambda concurrency limit is 10, because reserving any leaves fewer than 10 unreserved;
   pass `-var session_reserved_concurrency=-1 -var discord_reserved_concurrency=-1 -var janitor_reserved_concurrency=-1` there. An unreserved janitor can overlap itself, which its conditional transitions tolerate.
   The monthly budget notice is e-mailed: copy `infra/main/terraform.tfvars.example` to `terraform.tfvars` and set `budget_email`.
5. Updating the agent or node scripts: `mise run build`, then `terraform apply`. The node picks them up on its next boot; a new agent binary is promoted only after it reaches DynamoDB, otherwise the previous one is restored.
   Replacing the instance itself needs an explicit `terraform apply -replace=aws_instance.vpn` and ends live sessions.

### Discord

1. Create an application and a bot in the Developer Portal; invite the bot to your server with permission to send/edit messages in the approval channel.
2. Create the approval channel. Enable Developer Mode and copy the channel ID.
3. Store the values (Terraform creates placeholders and ignores later value changes):
   ```
   aws ssm put-parameter --region us-west-2 --overwrite --type SecureString --name /gamevpn/discord/bot_token --value '<bot token>'
   aws ssm put-parameter --region us-west-2 --overwrite --type String --name /gamevpn/discord/app_public_key --value '<application public key (hex)>'
   aws ssm put-parameter --region us-west-2 --overwrite --type String --name /gamevpn/discord/channel_id --value '<channel id>'
   ```
   Lambdas cache these for 5 minutes, so a rotated value takes effect without a redeploy.
4. Set the `discord_interactions_url` Terraform output as the Interactions Endpoint URL in the Developer Portal.
   Discord sends a signed PING on save, so the public key parameter must be set first.

## Who can use it

The session API is an `AWS_IAM` Function URL: a request without a valid SigV4 signature, or from a principal that may not call it, is rejected with 403 before the Lambda runs. The caller's identity is the Identity Center user name taken from the signed role session (`AWSReservedSSO_*/<user name>`); other roles are refused because they choose their own session name.

Terraform creates the `players` group and the `gamevpn-player` permission set (it can only call the session API) and assigns the group to the account. People are added by hand, which Terraform does not do for you: in the IAM Identity Center console create the user (this sends the invitation e-mail) and add it to `players`. Check members with `aws identitystore list-group-memberships --identity-store-id <id> --group-id <group id>`.

Function URL creation makes Lambda add its own public-access statements to the function policy. After switching a URL to `AWS_IAM`, remove the stale ones: `aws lambda remove-permission --function-name gamevpn-session --statement-id FunctionURLAllowPublicAccess` and the same for `FunctionURLAllowInvokeAction`.

## The client (macOS)

`mise run build` produces `build/maplegate`. Put the deployment's settings (none of them is secret) in `~/Library/Application Support/MapleGate/config.json`:

```
{"start_url": "<Identity Center start URL>", "region": "us-west-2", "account_id": "<account>", "role_name": "gamevpn-player", "api_url": "<session_api_url output>", "game_processes": ["<program name of the game>"]}
```

`build/maplegate up` asks for the administrator password once (the tunnel needs root), signs in in the browser the first time only, asks for a session, waits for the Discord approval, and brings up the tunnel. Ctrl-C closes the session and removes the routes. `maplegate up` is the terminal client; `maplegate login` only signs in. All traffic goes through the node, IPv6 included (the node drops it). Windows is not implemented yet.

## The installer (macOS)

`client/packaging/macos/build-pkg.sh <settings file>` builds `build/MapleGate.pkg` (the tray app, the daemon, `MapleGate.app` to open it from Spotlight, log rotation) and `build/MapleGate-uninstall.pkg`. The settings file is the JSON above; it is copied into the package, so a friend never writes it. What a friend does is in [docs/friends-guide.md](docs/friends-guide.md). The pkg is not signed, so the guide says how to get past Gatekeeper.

## Handing out the installer

Put the pkg in the artifacts bucket once per release, and register the commands once (this replaces every other command of the app):

```
aws s3 cp build/MapleGate.pkg s3://<artifacts bucket>/dist/MapleGate.pkg
scripts/register-discord-command.sh <application id>
```

A friend types `/download-macos` (or `/download-windows`, which answers "no installer yet" until `DIST_KEY_WINDOWS` is set) in the server and gets a link that only they can see and that works for 15 minutes. The discord Lambda signs it with its own role, which may read `dist/` and nothing else in the bucket. The pkg holds the deployment's non-secret settings, so anyone in the server can get it; remove the command or the bot's access to the channel to stop that.

## Debugging with the CLI (no tunnel)

```
aws sso login --profile <your profile>
export AWS_PROFILE=<your profile> MAPLEGATE_API_URL=<session_api_url output>
go run ./client/cmd/maplegate-cli up > wg0.conf
```
`up` creates a session and waits; press Approve in Discord. It then prints a WireGuard config to stdout (the private key stays on your machine). `get <id>` and `delete <id>` read and end a session. An unsigned `curl` to the URL must return 403.

Clients must treat only `denied`, `failed`, `closed` and `expired` as the end of a session. `provisioning` after `active`, or an HTTP 503, means the node or the table is briefly unreachable and the tunnel should stay up.

The node has no SSH and no inbound port besides WireGuard. For the first boots, use Session Manager (`aws ssm start-session --target <instance id>`) or `aws ec2 get-console-output` for the cloud-init log; once the node is proven, drop the permission with `-var node_session_manager=false`.

The live checks still open are the Todo list in [docs/e2e.md](docs/e2e.md).

## Development

`mise run pre-merge` formats and runs every check; `mise run build` builds the arm64 Lambda and agent binaries.

`internal/store` has one contract suite run against the in-memory store always and against DynamoDB when `DYNAMODB_ENDPOINT` points at DynamoDB Local; `mise run test-dynamodb` starts DynamoDB Local with Apple [`container`](https://github.com/apple/container) and runs it (CI uses a service container instead).
`lambda/internal/scenario` runs the handlers, the janitor and the agent together against random operation sequences (`go test -short` runs fewer).

`mise run test-integration` runs the integration tests locally with Apple [`container`](https://github.com/apple/container) on an Apple-silicon Mac (`container system start` first). It is not part of `mise run ci` because CI runners have no such VMs, and it is not an end-to-end test: the live checks above are.

| Production | In the integration tests |
|---|---|
| Lambda behind Function URLs | handlers called in-process (stand-in) |
| async self-invoke | goroutine calling `HandleAsync` (stand-in) |
| Discord signatures and API | self-signed requests, fake client (stand-in) |
| EventBridge Scheduler, every minute | janitor ticked every 2 seconds (stand-in) |
| DynamoDB | DynamoDB Local (compatible implementation) |
| EC2 start/stop, IMDS, IAM | container start/stop, fake IMDS, static credentials (stand-in) |
| kernel WireGuard | `wireguard-go` in userspace; the container kernel has no module, the agent's wgctrl calls are the same |
| AL2023, systemd, cloud-init, `setup.sh` | alpine and a small entrypoint (stand-in) |
| SSM Parameter Store, Identity Center, the client | not covered |

The real `vpn-agent`, the store, the handlers and the janitor are the real code. Covered: approve to active to delete with a ping through the tunnel, deny, idle power-off and restart, hard node stop, and DynamoDB unreachable from the node (blind mode, then catching up).

## License

Apache-2.0, see [LICENSE](LICENSE). Copyright 2026 ShotaKitazawa.
