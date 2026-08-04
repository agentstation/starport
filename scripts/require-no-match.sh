#!/bin/sh

set -u

status=0
"$@" >/dev/null || status=$?

case "$status" in
	1)
		exit 0
		;;
	0)
		exit 1
		;;
	*)
		exit "$status"
		;;
esac
