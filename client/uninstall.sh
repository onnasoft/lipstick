#!/bin/sh
# Removes the lipstick client service.
#
# Usage (as root):
#   sh uninstall.sh [--purge] [--root DIR]
#
#   (default)  stop and remove the service and the binary. The configuration
#              (which holds the domain key) and the service user are kept.
#   --purge    also remove /etc/lipstick-client and the service user.
#   --root DIR uninstall from DIR instead of /. For staging and tests.
set -eu

app=lipstick-client
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
  if systemctl list-unit-files "$app.service" >/dev/null 2>&1; then
    systemctl stop "$app" 2>/dev/null || true
    systemctl disable "$app" 2>/dev/null || true
  fi
fi

rm -f "${root}/etc/systemd/system/$app.service" "${root}/usr/bin/$app"

if [ "$staging" -eq 0 ]; then
  systemctl daemon-reload
fi

if [ "$purge" -eq 1 ]; then
  echo "Purging ${root}/etc/$app (configuration, domain key, CA file)."
  rm -rf "${root}/etc/$app"
  if [ "$staging" -eq 0 ] && id "$app" >/dev/null 2>&1; then
    userdel "$app"
  fi
else
  echo "Kept ${root}/etc/$app (configuration and domain key). Use --purge to remove it."
fi

echo "Uninstalled $app."
