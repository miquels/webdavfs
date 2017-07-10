
package main

import (
	"fmt"
	"os"
	"path"
	"strings"
	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	getopt "github.com/pborman/getopt/v2"
)

type Opts struct {
	Fake	bool
	NoMtab	bool
	Sloppy	bool
	Verbose	bool
	Option	map[string]string
}
var opts = Opts{ Option: map[string]string{} }
var progname = path.Base(os.Args[0])

var DefaultPath = "/usr/local/bin:/usr/local/sbin:/bin:/sbin:/usr/bin:/usr/sbin"

func usage(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
	}
	fmt.Fprintf(os.Stderr, "Usage: %s -sfnv -o opts url mountpoint\n", progname)
	os.Exit(1)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", progname, err)
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
		v := ""
		if len(kv) > 1 {
			v = kv[1]
		}
		opts.Option[kv[0]] = v
	}

	url := getopt.Arg(0)
	mountpoint := getopt.Arg(1)
	username := opts.Option["username"]
	password := opts.Option["password"]

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
		fatal(err)
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

	if _, ok := opts.Option["allow_root"]; ok {
		fmo = append(fmo, fuse.AllowRoot())
	}
	if _, ok := opts.Option["allow_other"]; ok {
		fmo = append(fmo, fuse.AllowOther())
	}
	if _, ok := opts.Option["async_read"]; ok {
		fmo = append(fmo, fuse.AsyncRead())
	}
	if _, ok := opts.Option["default_permissions"]; ok {
		fmo = append(fmo, fuse.DefaultPermissions())
	}
	if _, ok := opts.Option["nonempty"]; ok {
		fmo = append(fmo, fuse.AllowNonEmptyMount())
	}
	if _, ok := opts.Option["ro"]; ok {
		fmo = append(fmo, fuse.ReadOnly())
	}

	c, err := fuse.Mount(
		mountpoint,
		fmo...,
	)
	if err != nil {
		fatal(err)
	}
	defer c.Close()

	if IsDaemon() {
		Detach()
	}

	err = fs.Serve(c, NewFS(dav))
	if err != nil {
		fatal(err)
	}

	// check if the mount process has an error to report
	<-c.Ready
	if err := c.MountError; err != nil {
		fatal(err)
	}
}

