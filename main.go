
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
	Debug		bool
	Fake		bool
	NoMtab		bool
	Sloppy		bool
	Verbose		bool
	RawOptions	string
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
	stropts := []string{}
	for _, o := range strings.Split(opts.RawOptions, ",") {
		if strings.HasPrefix(o, "username=") {
			os.Setenv("WEBDAV_USERNAME", o[9:])
		} else if strings.HasPrefix(o, "password=") {
			os.Setenv("WEBDAV_PASSWORD", o[9:])
		} else {
			stropts = append(stropts, o)
		}
	}
	if len(stropts) > 0 {
		args = append(args, "-o" + strings.Join(stropts, ","))
	}
	os.Args = args
}

func parseUInt32(s string, opt string) uint32 {
	n, err := strconv.ParseUint(s , 10, 32)
	if err != nil {
		fatal(opt + " option: " + err.Error())
	}
	return uint32(n)
}

func main() {

	getopt.Flag(&opts.Debug, 'd', "enable debugging")
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

	err := getopt.Getopt(nil)
	if err != nil {
		usage(err)
	}

	// now the two non-options left are url and mountpoint.
	url := getopt.Arg(0)
	mountpoint := getopt.Arg(1)

	// parse -o option1,option2,option3=foo ..
	for _, o := range strings.Split(opts.RawOptions, ",") {
		kv := strings.SplitN(o, "=", 2)
		if len(kv) > 1 {
			opts.BoolOption[kv[0]] = true
			opts.StrOption[kv[0]] = kv[1]
		} else {
			opts.StrOption[kv[0]] = "true"
			opts.BoolOption[kv[0]] = true
		}
	}

	if strings.HasPrefix(progname, "mount.") {
		if !IsDaemon() {
			rebuildOptions(url, mountpoint)
			Daemonize()
		}
	}

	config := WebdavFS{}
	if os.Getuid() != 0 {
		config.Uid = uint32(os.Getuid())
		config.Gid = uint32(os.Getgid())
	}

	maxconns := 8
	maxidleconns := 8
	if opts.StrOption["maxconns"] != "" {
		maxconns = int(parseUInt32(opts.StrOption["maxconns"], "maxconns"))
	}
	if opts.StrOption["maxidleconns"] != "" {
		maxidleconns = int(parseUInt32(opts.StrOption["maxidleconns"], "maxidleconns"))
	}

	if opts.StrOption["uid"] != "" {
		uid := parseUInt32(opts.StrOption["uid"], "uid")
		if os.Getuid() != 0 && os.Getuid() != int(uid) {
			fatal("uid option: permission denied")
		}
		config.Uid = uid
	}
	if opts.StrOption["gid"] != "" {
		gid := parseUInt32(opts.StrOption["gid"], "gid")
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
		config.Gid = gid
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

	username := os.Getenv("WEBDAV_USERNAME")
	password := os.Getenv("WEBDAV_PASSWORD")
	if opts.StrOption["username"] != "" {
		username = opts.StrOption["username"]
	}
	if opts.StrOption["password"] != "" {
		password = opts.StrOption["password"]
	}
	os.Unsetenv("WEBDAV_USERNAME")
	os.Unsetenv("WEBDAV_PASSWORD")

	// for some reason we can end up without a $PATH ..
	if os.Getenv("PATH") == "" {
		os.Setenv("PATH", DefaultPath)
	}

	dav := &DavClient{
		Url: url,
		MaxConns: maxconns,
		MaxIdleConns: maxidleconns,
		Username: username,
		Password: password,
	}
	err = dav.Mount()
	if err != nil {
		fatal(err.Error())
	}
	if !dav.CanPutRange() && !opts.BoolOption["ro"] {
		fmt.Fprintf(os.Stderr, "%s: no PUT Range support, writing disabled\n", url)
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

