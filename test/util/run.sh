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


# A Pixel 8 stopped at "cannot mount /product" while NikGApps installed to the
# same partition. These cover the helpers that decide where a partition is and
# whether it will take a write.
echo "== finding the partition =="

DEV="$WORK/dev/block"
mkdir -p "$DEV/mapper" "$DEV/bootdevice/by-name" "$WORK/sys/block"
: > "$WORK/mounts"
: > "$WORK/cmdline"
getprop() { return 1; }

# Redirect the absolute paths these read at the point of definition, so the
# real bodies are under test against a fake /dev, /proc and /sys.
fake() {
  eval "$(sed -n "/^$1()/,/^}/p" "$UTIL" |
          sed -e "s#/dev/block#$DEV#g" \
              -e "s#/proc/mounts#$WORK/mounts#g" \
              -e "s#/proc/cmdline#$WORK/cmdline#g" \
              -e "s#/sys/block#$WORK/sys/block#g" \
              -e "s#\[ -b #[ -e #g")"
}
for fn in find_slot block_paths find_block block_for mount_reason make_rw; do fake "$fn"; done

: > "$DEV/mapper/product_b"
echo "$DEV/mapper/system_b /system_root ext4 ro 0 0" > "$WORK/mounts"
check "slot comes from what recovery mounted" "$(find_slot)" "_b"
check "finds the slot-suffixed mapper device" "$(find_block product)" "$DEV/mapper/product_b"

# The gap: mapper exists but does not carry this partition, so the search has
# to go on to by-name instead of giving up on the first base.
: > "$DEV/bootdevice/by-name/vendor"
check "falls through to by-name when mapper lacks it" \
  "$(find_block vendor)" "$DEV/bootdevice/by-name/vendor"
check "every base is searched" "$(block_paths | wc -l | tr -d ' ')" "2"

echo "== clearing force_ro on a dynamic partition =="
# What the device really looks like: the mapper name is a symlink to dm-5 and
# the sysfs knob is named for the dm device, not the link.
: > "$DEV/dm-5"
ln -sf "$DEV/dm-5" "$DEV/mapper/product_b"
mkdir -p "$WORK/sys/block/dm-5"
echo 1 > "$WORK/sys/block/dm-5/force_ro"
echo "$DEV/mapper/product_b /product ext4 ro 0 0" > "$WORK/mounts"

blockdev() { :; }
mount() { :; }
is_writable() { return 0; }
make_rw /product >/dev/null 2>&1
check "force_ro is cleared through the symlink" "$(cat "$WORK/sys/block/dm-5/force_ro")" "0"

echo "== mount_reason =="
echo "$DEV/mapper/product_b /product erofs ro 0 0" > "$WORK/mounts"
case "$(mount_reason /product)" in
  *read-only*) r=erofs ;; *) r="$(mount_reason /product)" ;;
esac
check "a read-only format says so" "$r" "erofs"

echo "$DEV/mapper/product_b /product ext4 ro 0 0" > "$WORK/mounts"
case "$(mount_reason /product)" in
  *"would not accept a write"*) r=write ;; *) r="$(mount_reason /product)" ;;
esac
check "a writable format blames the write" "$r" "write"

: > "$WORK/mounts"
case "$(mount_reason /nosuch)" in
  *"no block device named nosuch"*) r=missing ;; *) r="$(mount_reason /nosuch)" ;;
esac
check "a missing partition names what was searched" "$r" "missing"


# frp_wipe_block zeros the FRP structure and rewrites its digest. It writes a
# raw partition, so the range guard and the digest have to be exactly right.
echo "== frp_wipe_block =="
eval "$(sed -n '/^frp_wipe_block()/,/^}/p' "$UTIL")"

# stub the device-only helper; ui_print already goes nowhere useful here
blockdev() { case "$1" in --getsize64) stat -c %s "$2" ;; --setrw) : ;; esac; }
ui_print() { :; }

FRP="$WORK/frp.img"
python3 -c "open('$FRP','wb').write(b'\xff'*524288)"
frp_wipe_block "$FRP"

python3 - "$FRP" > "$WORK/frp.check" <<'PYEOF'
import sys, hashlib
d = bytearray(open(sys.argv[1],'rb').read())
n = len(d); oem = n-1; cred = oem-1000; test = cred-10000
secret = test-32; magic = secret-8
probe = bytearray(d); probe[0:32] = b'\x00'*32
print("magic=" + d[magic:magic+8].hex())
print("secret=" + ("zero" if set(d[secret:secret+32])=={0} else "nonzero"))
print("cred=" + ("zero" if set(d[cred:cred+1000])=={0} else "nonzero"))
print("digest=" + ("valid" if bytes(d[0:32])==hashlib.sha256(probe).digest() else "invalid"))
PYEOF
. "$WORK/frp.check"
check "magic bytes written"    "$magic"  "dac2fccdb91b0988"
check "secret region zeroed"   "$secret" "zero"
check "credential region zeroed" "$cred" "zero"
check "digest recomputed valid" "$digest" "valid"

# A partition outside the FRP size range must be left untouched, so a mis-set
# ro.frp.pst cannot zero something real.
BIG="$WORK/big.img"
python3 -c "open('$BIG','wb').write(b'\xaa'*(20*1024*1024))"
before=$(sha256sum "$BIG" | cut -d' ' -f1)
frp_wipe_block "$BIG"
after=$(sha256sum "$BIG" | cut -d' ' -f1)
check "20MB block left untouched" "$before" "$after"

TINY="$WORK/tiny.img"
python3 -c "open('$TINY','wb').write(b'\xaa'*4096)"
b2=$(sha256sum "$TINY" | cut -d' ' -f1); frp_wipe_block "$TINY"
a2=$(sha256sum "$TINY" | cut -d' ' -f1)
check "4KB block left untouched" "$b2" "$a2"

echo
[ "$fail" = 0 ] && echo "all util assertions passed" || echo "FAILURES"
exit $fail
