#!/bin/sh
export WLR_LIBINPUT_NO_DEVICES=1
[ -f /etc/pikiosk/session.env ] && . /etc/pikiosk/session.env
exec sway --config /etc/pikiosk/sway.conf
