# Server layout (file locations)

Everything lives in a tidy, predictable layout. You can also see this any time
from **Manage → File Locations** in the CLI.

| Path | What |
|------|------|
| `/root/night-watcher` | The release bundle and downloaded archives. |
| `/root/night-watcher/backups` | [Backup](backup-restore.md) `.tar.gz` files. |
| `/etc/night-watcher` | Tunnel configs (one `.toml` per tunnel) and runtime state. |
| `/usr/local/bin/night-watcher` | The binary itself. |
| `night-watcher-<name>.service` | A systemd unit per tunnel. |
| `night-watcher-monitor.service` | The [monitor service](monitor-service.md). |

The install directory is recorded in `/etc/night-watcher/install_path`, which is what
the uninstaller reads to know what to remove.

---
[← Back to the main README](../README.md)
