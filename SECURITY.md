# Security

Report vulnerabilities privately through [GitHub security advisories](https://github.com/XtremeOwnage/pikiosk/security/advisories/new), not in public issues.

## Model

- The web UI needs the admin password; the REST API needs the API token. Until a password is set, anyone on the network can open the setup page.
- The kiosk serves plain HTTP on the local network. Put it on a trusted network or VLAN.
- Chromium's DevTools port listens on loopback only.
- Stored passwords, tokens and header values are never returned by the API.
- SSH accepts keys only.
- kioskd runs as the unprivileged `kiosk` user. Privileged actions (network, timezone, reboot, updates) go through polkit rules that allow only those actions.
