# Architecture

MapleGate is a reference implementation for AWS, not a portable product: it uses DynamoDB, Lambda Function URLs with IAM auth, IAM Identity Center, EventBridge Scheduler, EC2, S3 and SSM Parameter Store directly, and nothing abstracts them. A provider other than AWS would be a rewrite of the Lambdas and the client's sign-in.

## Components

DynamoDB is the single source of truth; nothing is ever pushed to the node.

- `session` Lambda (Function URL): creates, reads and closes sessions. It only writes intent to DynamoDB.
- `discord` Lambda (Function URL): verifies Discord's signature and records approve / deny.
- `janitor` Lambda (EventBridge Scheduler, every minute): enforces deadlines, starts the node when sessions are waiting, frees IPs, and keeps Discord messages in sync.
- `vpn-agent` (on the EC2 node): converges WireGuard peers on the work queue in DynamoDB, reports what it applied, and powers the node off when nothing is live.
- A tunnel user can reach the internet through the node and nothing else: `node/nftables.conf` drops private and link-local destinations (the instance metadata service and the VPC among them) and everything addressed to the node except ping, and the metadata hop limit is 1. It also refuses port 25 (no spam from the node's address) and holds each peer to about 10 Mbps in each direction, so one friend's download does not take the node from the others.
- Node scripts and the agent binary ship through the artifacts bucket and are fetched on every boot, so changing them never replaces the instance (which would regenerate the server key).

How the node, the Lambdas and DynamoDB stay consistent, and what happens when each of them breaks, is specified in [lifecycle.md](lifecycle.md). The client plan is in [client.md](client.md).
