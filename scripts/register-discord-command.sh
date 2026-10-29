#!/bin/sh
# Registers /download once. The bot token goes to curl on stdin so it is neither printed nor in the process list.
set -eu
APP_ID=${1:?usage: register-discord-command.sh <application id>}
REGION=${AWS_REGION:-us-west-2}
PREFIX=${PARAM_PREFIX:-/gamevpn}

aws ssm get-parameter --region "$REGION" --with-decryption --name "$PREFIX/discord/bot_token" --query Parameter.Value --output text |
	sed 's/^/Authorization: Bot /' |
	curl -sS -f -X POST "https://discord.com/api/v10/applications/$APP_ID/commands" \
		-H @- -H 'Content-Type: application/json' \
		-d '{"name":"download","description":"Get a link to the MapleGate installer","type":1}' \
		-o /dev/null -w 'registered: HTTP %{http_code}\n'
