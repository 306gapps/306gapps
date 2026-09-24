#!/sbin/sh
# 306gapps addon.d survival script.
# Backs the install up before a ROM dirty-flash wipes /system, and restores it
# afterwards. The file list is written at install time.
#
# ADDOND_VERSION=3

. /tmp/backuptool.functions

LIST=/system/etc/306gapps/files.list

list_files() {
  [ -f "$LIST" ] || return 0
  # backuptool expects paths relative to /system.
  awk -F'\t' '{
    p = $1
    sub(/^system\//, "", p)
    print p
  }' "$LIST"
}

case "$1" in
  backup)
    list_files | while read -r f; do
      [ -n "$f" ] && backup_file "$S/$f"
    done
    backup_file "$S/etc/306gapps/files.list"
    backup_file "$S/etc/306gapps/release.txt"
    ;;
  restore)
    list_files | while read -r f; do
      [ -n "$f" ] && restore_file "$S/$f"
    done
    restore_file "$S/etc/306gapps/files.list"
    restore_file "$S/etc/306gapps/release.txt"
    ;;
  pre-backup|post-backup|pre-restore)
    ;;
  post-restore)
    ;;
esac
