#!/bin/sh
# Registers /download-macos and /download-windows (replacing any other command of the app). The bot token goes to curl on stdin so it is neither printed nor in the process list.
set -eu
APP_ID=${1:?usage: register-discord-command.sh <application id>}
REGION=${AWS_REGION:-${AWS_DEFAULT_REGION:?set AWS_REGION to the region of the deployment}}
PREFIX=${PARAM_PREFIX:-/gamevpn}

TOKEN=$(aws ssm get-parameter --region "$REGION" --with-decryption --name "$PREFIX/discord/bot_token" --query Parameter.Value --output text)

printf 'Authorization: Bot %s\n' "$TOKEN" |
	curl -sS -f -X PUT "https://discord.com/api/v10/applications/$APP_ID/commands" \
		-H @- -H 'Content-Type: application/json' \
		-d '[{"name":"download-macos","description":"Get a link to the macOS installer","type":1},{"name":"download-windows","description":"Get a link to the Windows installer","type":1}]' \
		-o /dev/null -w 'registered: HTTP %{http_code}\n'
