#!/bin/sh
# Based on https://nfpm.goreleaser.com/tips/
if ! command -V systemctl >/dev/null 2>&1; then
  echo "Could not find systemd. Skipping system installation." && exit 0
else
    systemd_version=$(systemctl --version | awk '/systemd /{print $2}')
fi

cleanInstall() {
    printf "Post Install of a clean install"

    if ! getent group  ripple >/dev/null 2>&1; then
        groupadd --system ripple
    fi
    # Create the user
    if ! id ripple > /dev/null 2>&1 ; then
        adduser --system --home /var/lib/ripple --gid "$(getent group ripple | awk -F ":" '{ print $3 }')" --shell /bin/false "ripple"
    fi
    
    mkdir -p /etc/ripple
    mkdir -p /var/lib/ripple
    chown ripple:ripple /var/lib/ripple

    # rhel/centos7 cannot use ExecStartPre=+ to specify the pre start should be run as root
    # even if you want your service to run as non root.
    if [ "${systemd_version}" -lt 231 ]; then
        printf "systemd version %s is less then 231, fixing the service file" "${systemd_version}"
        sed -i "s/=+/=/g" /etc/systemd/system/ripple.service
    fi
    printf "Reload the service unit from disk\n"
    systemctl daemon-reload ||:
}

upgrade() {
    :
}

action="$1"
if  [ "$1" = "configure" ] && [ -z "$2" ]; then
  # Alpine linux does not pass args, and deb passes $1=configure
  action="install"
elif [ "$1" = "configure" ] && [ -n "$2" ]; then
    # deb passes $1=configure $2=<current version>
    action="upgrade"
fi

case "${action}" in
  "1" | "install")
    cleanInstall
    ;;
  "2" | "upgrade")
    upgrade
    ;;
  *)
    # $1 == version being installed
    printf "Alpine"
    cleanInstall
    ;;
esac
