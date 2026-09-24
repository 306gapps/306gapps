#!/system/bin/sh
# Applies 306gapps build properties late in boot, where resetprop sticks.
MODDIR=${0%/*}
[ -s "$MODDIR/props.txt" ] || exit 0

until [ "$(getprop sys.boot_completed)" = "1" ]; do
  sleep 1
done

while IFS= read -r line; do
  [ -z "$line" ] && continue
  resetprop "${line%%=*}" "${line#*=}"
done < "$MODDIR/props.txt"
