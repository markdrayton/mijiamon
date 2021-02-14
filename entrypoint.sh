#!/bin/bash

/etc/init.d/dbus start
/etc/init.d/bluetooth start
exec mijiamon "$@"
