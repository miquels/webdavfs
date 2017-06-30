
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s MOUNTPOINT URL USERNAME PASSWORD\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 4 {
		usage()
		os.Exit(2)
	}
	mountpoint := flag.Arg(0)
	url := flag.Arg(1)
	username := flag.Arg(2)
	password := flag.Arg(3)

	dav := &DavClient{
		Url: url,
		Username: username,
		Password: password,
	}
	err := dav.Mount()
	if err != nil {
		log.Fatal(err)
	}

	c, err := fuse.Mount(
		mountpoint,
		fuse.FSName(url),
		fuse.Subtype("webdav"),
		fuse.VolumeName(url),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	err = fs.Serve(c, NewFS(dav))
	if err != nil {
		log.Fatal(err)
	}

	// check if the mount process has an error to report
	<-c.Ready
	if err := c.MountError; err != nil {
		log.Fatal(err)
	}
}

