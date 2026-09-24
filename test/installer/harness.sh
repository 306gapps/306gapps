#!/bin/sh
# Runs the real recovery installer against a fake ROM tree.
#
# Mounting, partition growth and SELinux labelling are device operations that
# cannot happen off-device, so those helpers are stubbed. Everything else --
# the API guard, space accounting, removals, extraction, size verification,
# build.prop edits, addon.d install and the install record -- is the real code.
set -e

TMP=$1          # unpacked installer/ lives here
ZIPFILE=$2      # the built recovery zip
export GAPPS_PREFIX=$3
OUTFD=1
export TMP ZIPFILE OUTFD

. "$TMP/installer/util.sh"

ui_print() { echo "$1"; }
log()      { [ "$VERBOSE" = "1" ] && echo "  . $1"; return 0; }
abort()    { echo "ABORT: $1" >&2; exit 1; }

# The fake tree is already "mounted"; just make sure the directory exists.
mount_part() { [ -d "$1" ] || mkdir -p "$1"; }
block_for()  { echo ""; }
grow_part()  { return 1; }
free_bytes() { echo "${FAKE_FREE:-1073741824}"; }

# chcon/chown need privileges we do not have and are not what is under test.
set_meta() { chmod "$2" "$1" 2>/dev/null; return 0; }

UTIL_LOADED=1
. "$TMP/installer/installer.sh"
