# Deployment

## Setup

### AWS

Administration (Terraform, the console) uses an IAM user in the `admin` group, which has only `AdministratorAccess`, signed in with `aws login`. Friends are Identity Center users and never get more than the `<name>-player` permission set (`name` is a variable of the module, `gamevpn` by default; the examples below use that). Identity Center's MFA setting covers everyone in the instance and is changed only in the console (Settings, Authentication); it is switched off here because the Discord approval is the second factor (without approval, see below, sign-in is the only factor, so turn it on).

Run `terraform plan` and read it before every `apply`.

1. Use a Paid-plan account. Free-plan accounts may not be able to enable Organizations / Identity Center; upgrade first if they cannot.
2. Enable AWS Organizations, then IAM Identity Center (organization instance).
3. Optional: a state bucket in S3 (names are global, so give `bucket` yours), made once with local state. Any other backend works as well:
   ```
   terraform -chdir=infra/bootstrap init && terraform -chdir=infra/bootstrap apply -var region=<region> -var bucket=<state bucket>
   ```
4. The infrastructure is a module, `infra/modules/maplegate`. `infra/main` is the smallest root that calls it; a deployment of your own calls the module from its own root (`source = "github.com/ShotaKitazawa/MapleGate//infra/modules/maplegate?ref=<version>"`) and passes `build_dir`, the directory of the binaries Terraform uploads and zips (`mise run build` writes `build/`). Region and credentials belong to the provider of that root. Build, then apply:
   ```
   mise run build
   terraform -chdir=infra/main init && terraform -chdir=infra/main apply
   ```
   Reserved concurrency (session 2 / discord 3 / janitor 1) fails on accounts whose Lambda concurrency limit is 10, because reserving any leaves fewer than 10 unreserved;
   pass `-var session_reserved_concurrency=-1 -var discord_reserved_concurrency=-1 -var janitor_reserved_concurrency=-1` there. An unreserved janitor can overlap itself, which its conditional transitions tolerate.
   Copy `infra/main/terraform.tfvars.example` to `terraform.tfvars` and set `region`, `artifacts_bucket` and `budget_email` (the monthly budget notice is e-mailed there). The other inputs, with their defaults, are in `infra/modules/maplegate/variables.tf`. The module's outputs include `client_settings`, the values the installers are built with.
5. Updating the agent or node scripts: `mise run build`, then `terraform apply`. The node picks them up on its next boot; a new agent binary is promoted only after it reaches DynamoDB, otherwise the previous one is restored.
   Replacing the instance itself needs an explicit `terraform apply -replace=aws_instance.vpn` and ends live sessions.

### Discord

Discord carries the approval button, the download commands and the node alerts. To run without the approval step, set the module input `require_discord_approval = false`: a signed-in player then gets a session at once, nothing is posted to Discord for it, and Identity Center sign-in is the only gate. The download commands and the alerts still need the bot.

1. Create an application and a bot in the Developer Portal; invite the bot to your server with permission to send/edit messages in the approval channel.
2. Create the approval channel. Enable Developer Mode and copy the channel ID.
3. Store the values (Terraform creates placeholders and ignores later value changes):
   ```
   aws ssm put-parameter --region <region> --overwrite --type SecureString --name /gamevpn/discord/bot_token --value '<bot token>'
   aws ssm put-parameter --region <region> --overwrite --type String --name /gamevpn/discord/app_public_key --value '<application public key (hex)>'
   aws ssm put-parameter --region <region> --overwrite --type String --name /gamevpn/discord/channel_id --value '<channel id>'
   ```
   Lambdas cache these for 5 minutes, so a rotated value takes effect without a redeploy.
4. Set the `discord_interactions_url` Terraform output as the Interactions Endpoint URL in the Developer Portal.
   Discord sends a signed PING on save, so the public key parameter must be set first.

## Who can use it

The session API is an `AWS_IAM` Function URL: a request without a valid SigV4 signature, or from a principal that may not call it, is rejected with 403 before the Lambda runs. The caller's identity is the Identity Center user name taken from the signed role session (`AWSReservedSSO_*/<user name>`); other roles are refused because they choose their own session name.

Terraform creates the `players` group (`players_group_name`) and the `gamevpn-player` permission set (it can only call the session API) and assigns the group to the account. People are added by hand, which Terraform does not do for you: in the IAM Identity Center console create the user (this sends the invitation e-mail) and add it to `players`. Check members with `aws identitystore list-group-memberships --identity-store-id <id> --group-id <group id>`.

Function URL creation makes Lambda add its own public-access statements to the function policy. After switching a URL to `AWS_IAM`, remove the stale ones: `aws lambda remove-permission --function-name gamevpn-session --statement-id FunctionURLAllowPublicAccess` and the same for `FunctionURLAllowInvokeAction`.

## Handing out the installer

The installers hold the deployment's settings, so they are built and uploaded by whoever runs the deployment, not by this repository. The settings file is the module's `client_settings` output plus `game_processes`, which is not infrastructure; the keys to upload to are the module's `dist_bucket`, `dist_key_macos` and `dist_key_windows` outputs. Per release, from the deployment:

```
client/packaging/macos/build-pkg.sh <settings file>
client/packaging/windows/build-installer.sh <settings file>
aws s3 cp build/MapleGate.pkg s3://<dist_bucket>/<dist_key_macos>
aws s3 cp build/MapleGate-Setup.exe s3://<dist_bucket>/<dist_key_windows>
```

Register the commands once (this replaces every other command of the app; `AWS_REGION` is the deployment's, `PARAM_PREFIX` is `/<name>` when `name` is not the default):

```
AWS_REGION=<region> scripts/register-discord-command.sh <application id>
```

A friend types `/download-macos` (or `/download-windows`, which answers "no installer yet" until `dist_key_windows` is set) in the server and gets a link that only they can see and that works for 15 minutes. The discord Lambda signs it with its own role, which may read `dist/` and nothing else in the bucket. The installer holds the deployment's non-secret settings, so anyone in the server can get it; remove the command or the bot's access to the channel to stop that.
