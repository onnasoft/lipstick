#!/bin/sh
# Installs the lipstick client as a systemd service.
#
# Usage (as root, from the client/ directory):
#   sh install.sh [--config FILE] [--ca FILE] [--root DIR]
#
#   --config FILE  configuration to install (default: ./config.yml). Copy
#                  config.yml.example to config.yml and fill in api_secret.
#   --ca FILE      PEM file of the server certificate or its CA. Copied next to
#                  the configuration as ca.pem. Needed for a self-signed server.
#   --root DIR     install under DIR instead of /. For staging and tests: no user
#                  is created, no service is enabled, and no ownership is set.
#
# The configuration is validated first. If it is not valid, nothing is installed.
set -eu

app=lipstick-client
config_src=config.yml
ca_src=""
root=""

usage() {
  sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
}

while [ $# -gt 0 ]; do
  case "$1" in
    --config) config_src=$2; shift 2 ;;
    --ca) ca_src=$2; shift 2 ;;
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
  echo "Run this from client/, and make sure the configuration file exists." >&2
  echo "Copy config.yml.example to config.yml and fill in api_secret first." >&2
  exit 1
fi
if [ -n "$ca_src" ] && [ ! -f "$ca_src" ]; then
  echo "CA file not found: $ca_src" >&2
  exit 1
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
go build -trimpath -o "$tmpdir/$app" ./

conf_dir="${root}/etc/$app"

# Two copies of the configuration. The check copy points ca_file at the CA in the
# temporary directory; the installed copy points at the CA in /etc. The check
# therefore runs against the file that will really be used.
check_conf="$tmpdir/check.yml"
install_conf="$tmpdir/config.yml"
grep -v '^ca_file:' "$config_src" > "$tmpdir/base.yml" || true
if [ -n "$ca_src" ]; then
  cp "$ca_src" "$tmpdir/ca.pem"
  cp "$tmpdir/base.yml" "$check_conf"
  cp "$tmpdir/base.yml" "$install_conf"
  echo "ca_file: \"$tmpdir/ca.pem\"" >> "$check_conf"
  echo "ca_file: \"/etc/$app/ca.pem\"" >> "$install_conf"
else
  cp "$config_src" "$check_conf"
  cp "$config_src" "$install_conf"
fi

if ! "$tmpdir/$app" -c "$check_conf" -check; then
  echo "The configuration is not valid. Nothing was installed." >&2
  exit 1
fi

if [ "$staging" -eq 0 ]; then
  if ! id "$app" >/dev/null 2>&1; then
    useradd --system --no-create-home --shell /usr/sbin/nologin "$app"
  fi
  if systemctl is-active --quiet "$app"; then
    systemctl stop "$app"
  fi
fi

install -D -m 0755 "$tmpdir/$app" "${root}/usr/bin/$app"
install -d -m 0750 "$conf_dir"
# The configuration holds the domain key: readable by root and the service only.
install -m 0640 "$install_conf" "$conf_dir/config.yml"
if [ -n "$ca_src" ]; then
  install -m 0644 "$tmpdir/ca.pem" "$conf_dir/ca.pem"
fi
install -D -m 0644 "$app.service" "${root}/etc/systemd/system/$app.service"

if [ "$staging" -eq 0 ]; then
  chown root:"$app" "$conf_dir" "$conf_dir/config.yml"
  systemctl daemon-reload
  systemctl enable --now "$app"
  systemctl --no-pager status "$app" || true
else
  echo "Staged under $root. Nothing was enabled."
fi

echo "Installed $app. Configuration: $conf_dir/config.yml"
