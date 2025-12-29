export const fstabPlaceholder = `devfs      /zroot/sylve/jails/<jail-id>/dev     devfs       rw      0       0
zroot/extra/dataset     /zroot/sylve/jails/<jail-id>/mnt/data        nullfs      rw      0       0`;

export const jailOptionsPlaceholder = `exec.poststop = "echo 'Jail <jail-name> has been stopped' >> /var/log/jail_events.log"`;
