
package main

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	getopt "github.com/pborman/getopt/v2"
)

type Opts struct {
	Fake		bool
	NoMtab		bool
	Sloppy		bool
	Verbose		bool
	StrOption	map[string]string
	BoolOption	map[string]bool
}
var opts = Opts{
	StrOption:	map[string]string{},
	BoolOption:	map[string]bool{},
}
var progname = path.Base(os.Args[0])

var DefaultPath = "/usr/local/bin:/usr/local/sbin:/bin:/sbin:/usr/bin:/usr/sbin"

func usage(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
	}
	fmt.Fprintf(os.Stderr, "Usage: %s -sfnv -o opts url mountpoint\n", progname)
	os.Exit(1)
}

func fatal(err string) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", progname, err)
	os.Exit(1)
}

func main() {

	if strings.HasPrefix(progname, "mount.") {
		if !IsDaemon() {
			Daemonize()
		}
	}

	var mopts string
	getopt.Flag(&opts.NoMtab, 'n', "do not uodate /etc/mtab (obsolete)")
	getopt.Flag(&opts.Sloppy, 's', "ignore unknown mount options")
	getopt.Flag(&opts.Fake, 'f', "do everything but the actual mount")
	getopt.Flag(&opts.Verbose, 'v', "be verbose")
	getopt.Flag(&mopts, 'o', "mount options")

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

	err := getopt.Getopt(nil)
	if err != nil {
		usage(err)
	}

	// parse -o option1,option2,option3=foo ..
	for _, o := range strings.Split(mopts, ",") {
		kv := strings.SplitN(o, "=", 2)
		if len(kv) > 1 {
			opts.BoolOption[kv[0]] = true
			opts.StrOption[kv[0]] = kv[1]
		} else {
			opts.StrOption[kv[0]] = "true"
			opts.BoolOption[kv[0]] = true
		}
	}

	config := WebdavFS{}
	if os.Getuid() != 0 {
		config.Uid = uint32(os.Getuid())
		config.Gid = uint32(os.Getgid())
	}

	if opts.StrOption["uid"] != "" {
		uid, err := strconv.ParseUint(opts.StrOption["uid"] , 10, 32)
		if err != nil {
			fatal("uid option: " + err.Error())
		}
		config.Uid = uint32(uid)
		if os.Getuid() != 0 && os.Getuid() != int(uid) {
			fatal("uid option: permission denied")
		}
	}
	if opts.StrOption["gid"] != "" {
		gid, err := strconv.ParseUint(opts.StrOption["gid"] , 10, 32)
		if err != nil {
			fatal("gid option: " + err.Error())
		}
		config.Gid = uint32(gid)
		if os.Getuid() != 0 {
			ok := false
			if os.Getgid() == int(gid) {
				ok = true
			}
			groups, err := os.Getgroups()
			if err == nil {
				for _, gr := range groups {
					if gr == int(gid) {
						ok = true
					}
				}
			}
			if !ok {
				fatal("gid option: permission denied")
			}
		}
	}

	if opts.StrOption["mode"] != "" {
		mode, err := strconv.ParseUint(opts.StrOption["mode"] , 8, 32)
		if err != nil {
			fatal("mode option: " + err.Error())
		}
		config.Mode = uint32(mode)
	}

	if opts.BoolOption["allow_other"] {
		if !opts.BoolOption["no_default_permissions"] {
			if config.Mode == 0 {
				config.Mode = 0755
			}
			opts.StrOption["default_permissions"] = "true"
			opts.BoolOption["default_permissions"] = true
		} else {
			if config.Mode == 0 {
				config.Mode = 0777
			}
		}
	}

	url := getopt.Arg(0)
	mountpoint := getopt.Arg(1)
	username := opts.StrOption["username"]
	password := opts.StrOption["password"]

	// for some reason we can end up without a $PATH ..
	if os.Getenv("PATH") == "" {
		os.Setenv("PATH", DefaultPath)
	}

	dav := &DavClient{
		Url: url,
		Username: username,
		Password: password,
	}
	err = dav.Mount()
	if err != nil {
		fatal(err.Error())
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

	if opts.BoolOption["allow_root"] {
		fmo = append(fmo, fuse.AllowRoot())
	}
	if opts.BoolOption["allow_other"] {
		fmo = append(fmo, fuse.AllowOther())
	}
	if opts.BoolOption["async_read"] {
		fmo = append(fmo, fuse.AsyncRead())
	}
	if opts.BoolOption["default_permissions"] {
		fmo = append(fmo, fuse.DefaultPermissions())
	}
	if opts.BoolOption["nonempty"] {
		fmo = append(fmo, fuse.AllowNonEmptyMount())
	}
	if opts.BoolOption["ro"] {
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

