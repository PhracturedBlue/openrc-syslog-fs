# openrc-syslog-fuse
A FUSE filesystem to make it easy to redirect openRC services to syslog

I like using Alpine with OpenRC on SBC systems, and like many of the features
of busybox's syslog service.  However, OpenRC does not make it easy to redirect
the stdout of services that don't have native syslog functionality to busybox
syslog.

This is a simple FUSE filesystem written in Go that allows creating a filehandle
and redirecting it to syslog.

The idea is to 1st start the FUSE filesystem as:

    service openrc_syslog_fs start

The idea is to create an OpenRC service like this:

    #!/sbin/openrc-run
    
    description="Manage ${SVCNAME} "
    command="some_command_name"
    command_args="some command arguments"
    output_log=/var/run/openrc_syslog_fs/${SVCNAME}.stdout
    error_log=/var/run/openrc_syslog_fs/${SVCNAME}.stderr

This will open 2 files on the FUSE filesystem: `<servicename>.stdout` and
`<servicename>.stderr`.  Any output to the stdout file will be sent to syslog
with priority LOG\_INFO and unit `servicename`.  Output to stderr will have
priority LOG\_ERR.  The unit-name can be whatever is desired, but file
extensions MUST be either `.stdout` or `.stderr`.

NOTE: That there is no reference-counting in the filesystem.  Once a file is
opened, it will result in a persistent syslog connection until the filesystem
is unmounted
