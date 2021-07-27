package main

import (
	"flag"
	"fmt"
	"log"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"syscall"
	"log/syslog"
	"os/user"
	"strconv"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"golang.org/x/net/context"
)

var progName = filepath.Base(os.Args[0])
var curUser, _ = user.Current()
var opts = Opts{}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", progName)
	fmt.Fprintf(os.Stderr, "  %s ZIP MOUNTPOINT\n", progName)
	flag.PrintDefaults()
}

func main() {
	log.SetFlags(0)
	log.SetPrefix(progName + ": ")

	slAddress := flag.String("syslog", "", "Syslog address: <tcp|udp>:<address>:port (default is local syslog)")
	perms := flag.Int("perms", 0200, "Filesystem permissions (will be masked with 0222).  Default 0200")
	debug := flag.Bool("debug", false, "Enable debugging")
	flag.Usage = usage
	flag.Parse()

	opts.Permissions = os.FileMode(*perms & 0222)
	if *slAddress == "" {
		opts.SyslogNetwork = ""
		opts.SyslogAddress = ""
	} else {
		var sl = strings.Split(*slAddress, ":")
		if len(sl) != 3 {
			log.Fatal("-syslog must match <protocol>:<address>:<port>")
			return
		}
		opts.SyslogNetwork = sl[0]
		opts.SyslogAddress = sl[1] + ":" + sl[2]
	}
	if *debug {
		fuse.Debug = func(msg interface{}) {
			fmt.Println(msg)
		}
	}

	if flag.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	mountpoint := flag.Arg(0)
	if err := mount(mountpoint); err != nil {
		log.Fatal(err)
	}
}

func mount(mountpoint string) error {
	c, err := fuse.Mount(mountpoint)
	if err != nil {
		return err
	}
	filesys := &FS{loggers : make(map[string]*syslog.Writer)}
	if err := fs.Serve(c, filesys); err != nil {
		return err
	}

	// check if the mount process has an error to report
	<-c.Ready
	if err := c.MountError; err != nil {
		return err
	}

	return nil
}

type Opts struct {
	SyslogNetwork string
	SyslogAddress string
	Permissions os.FileMode
}


/* Note that there is no reference counting.  Instead once a path is created, it will be peristent */
type FS struct {
	loggers map[string]*syslog.Writer
}

var _ fs.FS = (*FS)(nil)

func (f *FS) Root() (fs.Node, error) {
	n := &Dir{
		loggers: f.loggers,
	}
	return n, nil
}

type Dir struct {
	loggers map[string]*syslog.Writer
}

var _ fs.Node = (*Dir)(nil)

func (d *Dir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | 0755
	return nil
}

var _ = fs.NodeRequestLookuper(&Dir{})

func (d *Dir) Lookup(ctx context.Context, req *fuse.LookupRequest, resp *fuse.LookupResponse) (fs.Node, error) {
	path := req.Name
	if item, ok := d.loggers[path]; ok {
		child := &File{file: item}
		return child, nil
	}
	// path doesn't exist
	return nil, fuse.ENOENT
}

var _ = fs.HandleReadDirAller(&Dir{})

func (d *Dir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	var res []fuse.Dirent

	for name, _ := range d.loggers {
		var de fuse.Dirent
		de.Name = name
		res = append(res, de)
	}
	return res, nil
}

var _ = fs.NodeCreater(&Dir{})

func (d *Dir) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (fs.Node, fs.Handle, error) {
	println(d)
	//if !f.isDir() {
	//	return nil, nil, fuse.Errno(syscall.ENOTDIR)
	//}
	if req.Mode.IsDir() {
		return nil, nil, fuse.Errno(syscall.EISDIR)
	} else if !req.Mode.IsRegular() {
		return nil, nil, fuse.Errno(syscall.EINVAL)
	}

	parts := strings.Split(req.Name, ".")
	if len(parts) <= 1 {
		return nil, nil, errors.New("No using specified")
	}
	var priority syslog.Priority
	if parts[len(parts)-1] == "stderr" {
		priority = syslog.LOG_ERR
	} else if parts[len(parts)-1] == "stdout" {
		priority = syslog.LOG_INFO
	} else {
		return nil, nil, errors.New("Unsupported file extension")
	}
	var name = strings.Join(parts[:len(parts)-1], ".")
	var newf *File
	if item, ok := d.loggers[req.Name]; ok {
		newf = &File{file: item}
	} else {
		var logger, err = syslog.Dial(opts.SyslogNetwork, opts.SyslogAddress,
		                              priority|syslog.LOG_DAEMON, name)
		if err != nil {
			return nil, nil, err
		}
		d.loggers[req.Name] = logger
		newf = &File{file: logger}
	}
	resp.Flags |= fuse.OpenNonSeekable | fuse.OpenDirectIO
	var handle = &FileHandle{sl: newf.file}
	return newf, handle, nil
}

type File struct {
	file *syslog.Writer
}

var _ fs.Node = (*File)(nil)

func (f *File) Attr(ctx context.Context, a *fuse.Attr) error {
	var uid, _ = strconv.Atoi(curUser.Uid)
	var gid, _ = strconv.Atoi(curUser.Gid)
	a.Size   = 0
	a.Uid    = uint32(uid)
	a.Gid    = uint32(gid)
	a.Mode   = opts.Permissions
	a.Mtime  = time.Now()
	a.Ctime  = time.Now()
	a.Crtime = time.Now()
	return nil
}

var _ = fs.NodeOpener(&File{})

func (f *File) Open(ctx context.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	if req.Dir {
		println("Got dir open")
		return nil, errors.New("Dir request")
	}
	if req.Flags == fuse.OpenReadOnly || req.Flags == fuse.OpenReadWrite {
		return nil, errors.New("Read request")
	}
	resp.Flags |= fuse.OpenNonSeekable | fuse.OpenDirectIO
	return &FileHandle{sl: f.file}, nil
}

type FileHandle struct {
	sl *syslog.Writer
}

var _ fs.Handle = (*FileHandle)(nil)

var _ fs.HandleReleaser = (*FileHandle)(nil)

func (fh *FileHandle) Release(ctx context.Context, req *fuse.ReleaseRequest) error {
	return nil
}

/*
var _ = fs.HandleReader(&FileHandle{})

func (fh *FileHandle) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	return nil
}
*/
var _ = fs.HandleWriter(&FileHandle{})
func (fh *FileHandle) Write(ctx context.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	resp.Size = len(req.Data)
	fh.sl.Write(req.Data)
	return nil
}
