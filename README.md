# MapleGate

An on-demand WireGuard gateway on AWS, gated by IAM Identity Center sign-in and, optionally, a Discord approval button. A small node starts when someone is approved and stops itself when nobody is using it, so an idle deployment costs close to nothing. It was written to let a few friends reach a game's servers from one region.

This is a reference implementation for AWS, not a portable product: it uses DynamoDB, Lambda, IAM Identity Center, EC2 and other AWS services directly.

## How it works

1. A player signs in with IAM Identity Center and asks the session API for a session.
2. Someone presses Approve in Discord (this step can be turned off).
3. The node starts, adds the player as a WireGuard peer, and the client brings up the tunnel.
4. When nobody is using the node, it powers itself off.

Clients exist for macOS and Windows (tray app and installer); a CLI is there for debugging.

## Documentation

- [Architecture](docs/architecture.md): components and the safety properties of the node. [Lifecycle](docs/lifecycle.md) specifies how the node, the Lambdas and DynamoDB stay consistent.
- [Deployment](docs/deployment.md): AWS and Discord setup, the Terraform module, adding players, handing out the installer.
- [Using it](docs/usage.md): the client, the installers, the CLI.
- [Friends' guide (macOS)](docs/friends-guide.md) and [(Windows)](docs/friends-guide-windows.md): what a player does.
- [Development](docs/development.md): tests and the local test setup. [Client plan](docs/client.md); [live checks](docs/e2e.md).

## License

Apache-2.0, see [LICENSE](LICENSE). Copyright 2026 ShotaKitazawa.
