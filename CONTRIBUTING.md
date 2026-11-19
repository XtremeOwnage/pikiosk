# Contributing

Issues and pull requests are welcome.

- `go vet ./...` and `go test ./...` must pass.
- Changes to the browser, kiosk, MQTT or web packages should keep `nix build .#checks.x86_64-linux.vm` passing. It boots the full stack in QEMU.
- Shell scripts must pass `shellcheck -S warning`.
- New API endpoints and MQTT commands need an entry in `internal/web/api.md` and `docs-site/docs/reference/mqtt.md`.

By contributing you agree that your contributions are licensed under the MIT license.
