#!/bin/sh
# Removes the lipstick server service.
#
# Usage (as root):
#   sh uninstall.sh [--purge] [--root DIR]
#
#   (default)  stop and remove the service and the binary. The configuration,
#              the certificates and the service user are kept.
#   --purge    also remove /etc/lipstick (configuration and certificates) and the
#              service user. The database is not touched.
#   --root DIR uninstall from DIR instead of /. For staging and tests.
set -eu

app=lipstick
# The systemd unit has its own name. The binary, the configuration and the
# service user keep the name lipstick.
unit=lipstick-server
purge=0
root=""

while [ $# -gt 0 ]; do
  case "$1" in
    --purge) purge=1; shift ;;
    --root) root=$2; shift 2 ;;
    -h|--help) sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

staging=0
if [ -n "$root" ]; then staging=1; fi

if [ "$staging" -eq 0 ] && [ "$(id -u)" -ne 0 ]; then
  echo "Run as root." >&2
  exit 1
fi

if [ "$staging" -eq 0 ]; then
  if systemctl list-unit-files "$unit.service" >/dev/null 2>&1; then
    systemctl stop "$unit" 2>/dev/null || true
    systemctl disable "$unit" 2>/dev/null || true
  fi
fi

rm -f "${root}/etc/systemd/system/$unit.service" "${root}/usr/bin/$app"

if [ "$staging" -eq 0 ]; then
  systemctl daemon-reload
fi

if [ "$purge" -eq 1 ]; then
  echo "Purging ${root}/etc/$app (configuration, certificates)."
  rm -rf "${root}/etc/$app"
  if [ "$staging" -eq 0 ] && id "$app" >/dev/null 2>&1; then
    userdel "$app"
  fi
else
  echo "Kept ${root}/etc/$app (configuration and certificates). Use --purge to remove it."
fi

echo "Uninstalled $app."
