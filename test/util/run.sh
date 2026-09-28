#!/usr/bin/env bash
# Unit tests for the recovery helpers that the installer suite stubs out.
#
# The installer harness replaces free_bytes and mount_part with fakes, because
# they touch the device. That left the real implementations untested, and one
# of them mis-parsed busybox df on a real phone and reported 22 KB free for a
# partition with 2.4 GiB. These exercise the real code against captured output.
set -u

HERE=$(cd "$(dirname "$0")" && pwd)
UTIL="$HERE/../../internal/build/templates/recovery/util.sh"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

fail=0
check() {
  if [ "$2" = "$3" ]; then
    echo "  ok   $1"
  else
    echo "  FAIL $1: want $3, got $2"
    fail=1
  fi
}

mkdir -p "$WORK/bin"
export PATH="$WORK/bin:$PATH"

# A stand-in df whose output is whatever the test wrote to $WORK/df.out.
cat > "$WORK/bin/df" <<'EOF'
#!/bin/sh
cat "$WORK_DF"
EOF
chmod +x "$WORK/bin/df"
export WORK_DF="$WORK/df.out"

# Only free_bytes is under test; the rest of util.sh touches the device.
free_bytes() {
  df -k "$1" 2>/dev/null | tail -n1 | awk '{print $(NF-2) * 1024}'
}
eval "$(sed -n '/^free_bytes()/,/^}/p' "$UTIL")"

echo "== free_bytes =="

# A long device name makes busybox df wrap the columns onto the next line, so
# the available figure is no longer the fourth field. Captured from a Pixel 5
# in recovery with /product mounted.
cat > "$WORK/df.out" <<'EOF'
Filesystem           1K-blocks      Used Available Use% Mounted on
/dev/block/mapper/product_b
                       3195212    697264   2481564  22% /product
EOF
check "wrapped row reads the available column" "$(free_bytes /product)" "2541121536"

# The ordinary unwrapped shape still has to work.
cat > "$WORK/df.out" <<'EOF'
Filesystem           1K-blocks      Used Available Use% Mounted on
tmpfs                  3809396      3396   3806000   0% /tmp
EOF
check "unwrapped row reads the available column" "$(free_bytes /tmp)" "3897344000"

# A path that is not on a real mount: df falls back to the rootfs and reports
# nothing free, which is a legitimate zero rather than a parse failure.
cat > "$WORK/df.out" <<'EOF'
Filesystem           1K-blocks      Used Available Use% Mounted on
none                         0         0         0   0% /
EOF
check "an unmounted path reports zero" "$(free_bytes /product)" "0"

# GNU coreutils df, for anyone running the installer off-device.
cat > "$WORK/df.out" <<'EOF'
Filesystem     1K-blocks    Used Available Use% Mounted on
/dev/nvme1n1p1 499963392 1048576 498914816   1% /
EOF
check "coreutils output" "$(free_bytes /)" "510888771584"


# Recovery's shell compares in 32 bits: 2541121536 wraps to -1753845760, so a
# partition with 2.4 GiB free looked smaller than a 1.7 GiB payload and the
# installer refused to write to it. Every size comparison goes through lt().
echo "== lt, on sizes recovery's own [ cannot compare =="
eval "$(sed -n '/^lt()/,/^}/p' "$UTIL")"
eval "$(sed -n '/^sub_bytes()/,/^}/p' "$UTIL")"
eval "$(sed -n '/^add_bytes()/,/^}/p' "$UTIL")"

lt 2541121536 1832011106 && r=yes || r=no
check "2.4 GiB is not less than 1.7 GiB" "$r" "no"
lt 1832011106 2541121536 && r=yes || r=no
check "1.7 GiB is less than 2.4 GiB" "$r" "yes"
lt 3221225472 4294967296 && r=yes || r=no
check "3 GiB is less than 4 GiB" "$r" "yes"
lt 4294967296 3221225472 && r=yes || r=no
check "4 GiB is not less than 3 GiB" "$r" "no"
lt 100 200 && r=yes || r=no
check "small numbers still work" "$r" "yes"
lt 200 200 && r=yes || r=no
check "equal is not less" "$r" "no"

check "subtraction past 2 GiB" "$(sub_bytes 5000000000 1000000000)" "4000000000"
check "addition past 2 GiB"    "$(add_bytes 3000000000 2000000000)" "5000000000"

echo
[ "$fail" = 0 ] && echo "all util assertions passed" || echo "FAILURES"
exit $fail
