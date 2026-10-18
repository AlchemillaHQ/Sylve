# Autostart only on the primary VT; csh roots use .login instead.
if [ -n "${TERM:-}" ] && [ "$(tty 2>/dev/null)" = "/dev/ttyv0" ]; then
	exec /usr/local/bin/kiosk-launch
fi
