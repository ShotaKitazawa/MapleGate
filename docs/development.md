# Development

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
