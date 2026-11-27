#!/bin/sh
# Installs the lipstick server as a systemd service.
#
# Usage (as root, from the server/ directory):
#   sh install.sh [--config FILE] [--root DIR]
#
#   --config FILE  configuration to install (default: ./config.yml)
#   --root DIR     install under DIR instead of /. For staging and tests: no user
#                  is created, no service is enabled, and no ownership is set.
#
# The configuration is validated first. If it is not valid, nothing is installed.
set -eu

app=lipstick
# The systemd unit has its own name. The binary, the configuration and the
# service user keep the name lipstick.
unit=lipstick-server
config_src=config.yml
root=""

usage() {
  sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'
}

while [ $# -gt 0 ]; do
  case "$1" in
    --config) config_src=$2; shift 2 ;;
    --root) root=$2; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

staging=0
if [ -n "$root" ]; then staging=1; fi

if [ "$staging" -eq 0 ] && [ "$(id -u)" -ne 0 ]; then
  echo "Run as root: this script installs a system service." >&2
  exit 1
fi
if [ ! -f main.go ] || [ ! -f "$config_src" ]; then
  echo "Run this from server/, and make sure the configuration file exists." >&2
  echo "Copy config.yml.example to config.yml and fill in the values first." >&2
  exit 1
fi

# Build into a private temporary directory. A fixed path in /tmp can be
# replaced by another user through a symlink.
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
go build -trimpath -o "$tmpdir/$app" .

# Validate before touching the system. -check loads and validates the file and
# exits without opening anything.
if ! "$tmpdir/$app" -c "$config_src" -check; then
  echo "The configuration is not valid. Nothing was installed." >&2
  exit 1
fi

conf_dir="${root}/etc/$app"

if [ "$staging" -eq 0 ]; then
  if ! id "$app" >/dev/null 2>&1; then
    useradd --system --no-create-home --shell /usr/sbin/nologin "$app"
  fi
  if systemctl is-active --quiet "$unit"; then
    systemctl stop "$unit"
  fi
fi

install -D -m 0755 "$tmpdir/$app" "${root}/usr/bin/$app"
install -d -m 0750 "$conf_dir"
install -d -m 0750 "$conf_dir/certs"
# The configuration holds secrets: readable by root and the service user only.
install -m 0640 "$config_src" "$conf_dir/config.yml"
install -D -m 0644 "$unit.service" "${root}/etc/systemd/system/$unit.service"

if [ "$staging" -eq 0 ]; then
  chown root:"$app" "$conf_dir" "$conf_dir/config.yml"
  chown "$app:$app" "$conf_dir/certs"
  systemctl daemon-reload
  systemctl enable --now "$unit"
  systemctl --no-pager status "$unit" || true
else
  echo "Staged under $root. Nothing was enabled."
fi

echo "Installed $app. Configuration: $conf_dir/config.yml"
