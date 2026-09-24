if [ "$(id -un 2>/dev/null)" = "minerdash" ] &&
   [ "$(tty 2>/dev/null)" = "/dev/tty1" ] &&
   [ -x /usr/local/sbin/minerdash-console-menu ]; then
  exec /usr/local/sbin/minerdash-console-menu
fi
