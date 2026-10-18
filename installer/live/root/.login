# Same display selection as .profile, for csh-based live roots.
if ( "$tty" == "ttyv0" ) then
	exec /usr/local/bin/kiosk-launch
endif
