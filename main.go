
package main

import (
	"fmt"
	"os"
	"path"
	"strings"
	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/pborman/getopt/v2"
)

type Opts struct {
	Type		string
	TraceOpts	string
	TraceFile	string
	Daemonize	bool
	Fake		bool
	NoMtab		bool
	Sloppy		bool
	Verbose		bool
	RawOptions	string
}
var opts = Opts{}
var mountOpts MountOptions
var progname = path.Base(os.Args[0])

var DefaultPath = "/usr/local/bin:/usr/local/sbin:/bin:/sbin:/usr/bin:/usr/sbin"

func usage(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
	}
	fmt.Fprintf(os.Stderr, "Usage: %s [-sf] [-D] [-T opts] [-F file] [-o opts] url mountpoint\n", progname)
	fmt.Fprintf(os.Stderr, "       -s:         ignore unknown mount options\n")
	fmt.Fprintf(os.Stderr, "       -f:         don't actually mount\n")
	fmt.Fprintf(os.Stderr, "       -D:         daemonize (default when called as mount.*)\n")
	fmt.Fprintf(os.Stderr, "       -T opts:    trace options\n")
	fmt.Fprintf(os.Stderr, "       -F file:    trace file\n")
	fmt.Fprintf(os.Stderr, "       -o opts:    mount options\n")
	os.Exit(1)
}

func fatal(err string) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", progname, err)
	os.Exit(1)
}

// rebuild the os.Args array, string username/password.
func rebuildOptions(url, path string) {
	args := []string{ os.Args[0], url, path }
	bools := ""
	if opts.NoMtab {
		bools += "n"
	}
	if opts.Sloppy {
		bools += "s"
	}
	if opts.Fake {
		bools += "f"
	}
	if opts.Verbose {
		bools += "v"
	}
	if bools != "" {
		args = append(args, "-" + bools)
	}
	if opts.Type != "" {
		args = append(args, "-t" + opts.Type)
	}
	if opts.TraceOpts != "" {
		args = append(args, "-T" + opts.TraceOpts)
	}
	if opts.TraceFile != "" {
		args = append(args, "-F" + opts.TraceFile)
	}
	stropts := []string{}
	for _, o := range strings.Split(opts.RawOptions, ",") {
		if o == "" {
			continue
		}
		if strings.HasPrefix(o, "username=") {
			os.Setenv("WEBDAV_USERNAME", o[9:])
		} else if strings.HasPrefix(o, "password=") {
			os.Setenv("WEBDAV_PASSWORD", o[9:])
		} else if strings.HasPrefix(o, "cookie=") {
			os.Setenv("WEBDAV_COOKIE", o[7:])
		} else {
			stropts = append(stropts, o)
		}
	}
	if len(stropts) > 0 {
		args = append(args, "-o" + strings.Join(stropts, ","))
	}
	os.Args = args
}

func main() {

	// make sure stdin/out/err are open and valid.
	fd := -1
	var file *os.File
	var err error
	for fd < 3 {
		file, err = os.OpenFile("/dev/null", os.O_RDWR, 0666)
                if err != nil {
			fatal(err.Error())
		}
		fd = int(file.Fd())
	}
	file.Close()

	getopt.Flag(&opts.Type, 't', "type.subtype")
	getopt.Flag(&opts.Daemonize, 'D', "daemonize")
	getopt.Flag(&opts.TraceFile, 'F', "trace file")
	getopt.Flag(&opts.TraceOpts, 'T', "trace options")
	getopt.Flag(&opts.NoMtab, 'n', "do not uodate /etc/mtab (obsolete)")
	getopt.Flag(&opts.Sloppy, 's', "ignore unknown mount options")
	getopt.Flag(&opts.Fake, 'f', "do everything but the actual mount")
	getopt.Flag(&opts.Verbose, 'v', "be verbose")
	getopt.Flag(&opts.RawOptions, 'o', "mount options")

	// put non-option arguments last.
	l := len(os.Args)
	if l > 2 && !strings.HasPrefix(os.Args[1], "-") &&
		    !strings.HasPrefix(os.Args[2], "-") {
		// os.Args = append([]string{}, os.Args[:1]..., os.Args[3:]..., os.Args[1:3]...)
		args := []string{}
		args = append(args, os.Args[0])
		args = append(args, os.Args[3:]...)
		args = append(args, os.Args[1:3]...)
		os.Args = args
	}

	// check that we have two non-option args at the end
	if l < 3 || strings.HasPrefix(os.Args[l-2], "-") ||
	            strings.HasPrefix(os.Args[l-1], "-") {
		usage(nil)
	}

	err = getopt.Getopt(nil)
	if err != nil {
		usage(err)
	}

	// now the two non-options left are url and mountpoint.
	url := getopt.Arg(0)
	mountpoint := getopt.Arg(1)

	// parse mount options, then add defaults.
	mountOpts, err := parseMountOptions(opts.RawOptions, opts.Sloppy)
	if err != nil {
		fatal(err.Error())
	}
	if mountOpts.MaxConns == 0 {
		mountOpts.MaxConns = 8
	}
	if mountOpts.MaxIdleConns == 0 {
		mountOpts.MaxIdleConns = 8
	}

	if strings.HasPrefix(progname, "mount.") || opts.Daemonize {
		if !IsDaemon() {
			rebuildOptions(url, mountpoint)
			Daemonize()
		}
	}

	err = traceOpts(opts.TraceOpts, opts.TraceFile)
	if err != nil {
		fatal(err.Error())
	}

	config := WebdavFS{}
	if os.Getuid() != 0 {
		config.Uid = uint32(os.Getuid())
		config.Gid = uint32(os.Getgid())
	}
	config.Mode = mountOpts.Mode

	// if running from fstab with "uid=123,gid=456" set some reasonable
	// defaults so that that uid can actually access the files.
	if os.Getuid() == 0 && mountOpts.Uid != 0 && mountOpts.Mode == 0 {
		mountOpts.AllowOther = true
		config.Mode = 0700
	}

	if mountOpts.Uid > 0 {
		if os.Getuid() != 0 && os.Getuid() != int(mountOpts.Uid) {
			fatal("uid option: permission denied")
		}
		config.Uid = mountOpts.Uid
	}
	if mountOpts.Gid > 0 {
		if os.Getuid() != 0 {
			ok := false
			if os.Getgid() == int(mountOpts.Gid) {
				ok = true
			}
			groups, err := os.Getgroups()
			if err == nil {
				for _, gr := range groups {
					if gr == int(mountOpts.Gid) {
						ok = true
					}
				}
			}
			if !ok {
				fatal("gid option: permission denied")
			}
		}
		config.Gid = mountOpts.Gid
	}

	if mountOpts.AllowOther {
		if !mountOpts.NoDefaultPermissions {
			if config.Mode == 0 {
				config.Mode = 0755
			}
			mountOpts.DefaultPermissions = true
		} else {
			if config.Mode == 0 {
				config.Mode = 0777
			}
		}
	}

	username := os.Getenv("WEBDAV_USERNAME")
	password := os.Getenv("WEBDAV_PASSWORD")
	cookie   := os.Getenv("WEBDAV_COOKIE")
	if mountOpts.Username != "" {
		username = mountOpts.Username
	}
	if mountOpts.Password != "" {
		password = mountOpts.Password
	}
	if mountOpts.Cookie != "" {
		cookie = mountOpts.Cookie
	}
	os.Unsetenv("WEBDAV_USERNAME")
	os.Unsetenv("WEBDAV_PASSWORD")
	os.Unsetenv("WEBDAV_COOKIE")

	// for some reason we can end up without a $PATH ..
	if os.Getenv("PATH") == "" {
		os.Setenv("PATH", DefaultPath)
	}

	dav := &DavClient{
		Url: url,
		MaxConns: int(mountOpts.MaxConns),
		MaxIdleConns: int(mountOpts.MaxIdleConns),
		Username: username,
		Password: password,
		Cookie: cookie,
		PutDisabled: mountOpts.ReadWriteDirOps,
	}
	err = dav.Mount()
	if err != nil {
		fatal(err.Error())
	}
	if !dav.CanPutRange() && !mountOpts.ReadOnly && !mountOpts.ReadWriteDirOps {
		fmt.Fprintf(os.Stderr, "%s: no PUT Range support, mounting read-only\n", url)
		mountOpts.ReadOnly = true
	}
	if opts.Fake {
		return
	}

	fmo := []fuse.MountOption{
		fuse.FSName(url),
		fuse.Subtype("webdavfs"),
		fuse.VolumeName(url),
		fuse.MaxReadahead(1024 * 1024),
	}

	if mountOpts.AllowRoot {
		fmo = append(fmo, fuse.AllowRoot())
	}
	if mountOpts.AllowOther {
		fmo = append(fmo, fuse.AllowOther())
	}
	if mountOpts.AsyncRead {
		fmo = append(fmo, fuse.AsyncRead())
	}
	if mountOpts.DefaultPermissions {
		fmo = append(fmo, fuse.DefaultPermissions())
	}
	if mountOpts.NonEmpty {
		fmo = append(fmo, fuse.AllowNonEmptyMount())
	}
	if mountOpts.ReadOnly {
		fmo = append(fmo, fuse.ReadOnly())
	}

	c, err := fuse.Mount(
		mountpoint,
		fmo...,
	)
	if err != nil {
		fatal(err.Error())
	}
	defer c.Close()

	if IsDaemon() {
		Detach()
	}
	traceredirectStdoutErr()

	err = fs.Serve(c, NewFS(dav, config))
	if err != nil {
		fatal(err.Error())
	}

	// check if the mount process has an error to report
	<-c.Ready
	if err := c.MountError; err != nil {
		fatal(err.Error())
	}
}

