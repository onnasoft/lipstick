#!/bin/sh
# Installs lipstick as a systemd service. Run it as root from the server/
# directory, after creating server/config.yml from config.yml.example.
#
# Any failure stops the script with a message. Nothing is hidden.
set -eu

app=lipstick
conf_dir=/etc/$app

if [ "$(id -u)" -ne 0 ]; then
  echo "Run as root: this script installs a system service." >&2
  exit 1
fi

if [ ! -f main.go ] || [ ! -f config.yml ]; then
  echo "Run this from server/, and make sure config.yml exists there." >&2
  echo "Copy config.yml.example to config.yml and fill in the values first." >&2
  exit 1
fi

# Build into a private temporary directory. A fixed path in /tmp can be
# replaced by another user through a symlink.
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
go build -trimpath -o "$tmpdir/$app" .

# A dedicated system user, so the service does not run as root.
if ! id "$app" >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin "$app"
fi

if systemctl is-active --quiet "$app"; then
  systemctl stop "$app"
fi

install -m 0755 "$tmpdir/$app" "/usr/bin/$app"

install -d -m 0750 -o root -g "$app" "$conf_dir"
# The configuration holds secrets: readable by root and the service user only.
install -m 0640 -o root -g "$app" config.yml "$conf_dir/config.yml"
# Generated certificates are written here, so the service user owns this directory.
install -d -m 0750 -o "$app" -g "$app" "$conf_dir/certs"

install -m 0644 "$app.service" "/etc/systemd/system/$app.service"
systemctl daemon-reload
systemctl enable --now "$app"
systemctl --no-pager status "$app"
