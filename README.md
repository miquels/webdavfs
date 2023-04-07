# webdavfs

## A FUSE filesystem for WEBDAV shares.

Most filesystem drivers for Webdav shares act somewhat like a mirror;
if a file is read it's first downloaded then cached in its entirety
on a local drive, then read from there. Writing files is similar or
even worse- a partial update to a file might involve downloading it first,
modifying it, then uploading it again. In many cases that is not optimal.

This filesystem driver behaves like a network filesystem. It doesn't
cache anything locally, it just sends out partial reads/writes over the
network.

For that to work, you need partial write support- and unfortunately,
there is no standard for that. See
https://blog.sphere.chronosempire.org.uk/2012/11/21/webdav-and-the-http-patch-nightmare

However, there is support in Apache (the webserver, using mod_dav) and
[SabreDav](SABREDAV-partialupdate.md) (a php webserver server library,
used by e.g. NextCloud) for partial writes. So we detect if it's Apache or
SabreDav we're talking to and then use their specific methods to partially
update files.

If no support for partial writes is detected, mount.webdavfs will
print a warning and mount the filesystem read-only. In that case you can
also use the `rwdirops` mount option, this will make metadata writable
(i.e. you can use rm / mv / mkdir / rmdir) but you still won't be able
to write to files.

But if you only need to read files it's still way faster than davfs2 :)

## What is working

Basic filesystem operations.

- files: create/delete/read/write/truncate/seek
- directories: mkdir rmdir readdir
- query filesystem size (df / vfsstat)

## What is not yet working

- locking

## What will not ever work

- change permissions (all files are 644, all dirs are 755)
- change user/group
- devices / fifos / chardev / blockdev etc
- truncate(2) / ftruncate(2) for lengths between 1 .. currentfilesize - 1

This is basically because these are mostly just missing properties
from webdav.

## What platforms does it run on

- Linux
- FreeBSD (untested, but should work)
- It might work on macos if you use [osxfuse 3](https://github.com/osxfuse/osxfuse/releases/tag/osxfuse-3.11.2). Then again it might not. This is completely unsupported. See also [this issue](https://github.com/miquels/webdavfs/issues/11).

## How to install and use.

First you need to install golang, git, fuse, and set up your environment.
For Debian:

```
$ sudo -s
Password:
# apt-get install golang git fuse
# exit
```

Now with go and git installed, get a copy of this github repository:

```
$ git clone https://github.com/miquels/webdavfs
$ cd webdavfs
```

You're now ready to build the binary:

```
$ go get
$ go build
```

And install it:

```
$ sudo -s
Password:
# cp webdavfs /sbin/mount.webdavfs
```

Using it is simple as:
```
# mount -t webdavfs -ousername=you,password=pass https://webdav.where.ever/subdir /mnt
```

## Command line options

| Option | Description |
| --- | --- |
| -f | don't actually mount |
| -D | daemonize | default when called as mount.* |
| -T opts | trace options: fuse,webdav,httpreq,httphdr |
| -F file | trace file. file will be reopened when renamed, tracing will stop when file is removed |
| -o opts | mount options |

## Mount options

| Option | Description |
| --- | --- |
| allow_root		| If mounted as normal user, allow access by root |
| allow_other		| Allow access by others than the mount owner. This |
|			| also sets "default_permisions" |
| default_permissions	| As per fuse documentation |
| no_default_permissions | Don't set "default_permissions" with "allow_other" |
| ro			| Read only |
| rwdirops		| Read-write for directory operations, but no file-writing (no PUT) |
| rw			| Read-write (default) |
| uid			| User ID for filesystem |
| gid			| Group ID for filesystem. |
| mode			| Mode for files/directories on the filesystem (600, 666, etc). |
|			| Files will never have the executable bit on, directories always. |
| cookie		| Authorization Cookie (Useful for O365 Sharepoint/OneDrive for Business) |
| password		| Password of webdav user |
| username		| Username of webdav user |
| async_read		| As per fuse documentation |
| nonempty		| As per fuse documentation |
| maxconns              | Maximum number of parallel connections to the webdav
|                       | server (default 8)
| maxidleconns          | Maximum number of idle connections (default 8)
| sabredav_partialupdate | Use the sabredav partialupdate protocol even when
|                        | the remote server doesn't advertise support (DANGEROUS)

If the webdavfs program is called via `mount -t webdavfs` or as `mount.webdav`,
it will fork, re-exec and run in the background. In that case it will remove
the username and password options from the command line, and communicate them
via the environment instead.

The environment options for username and password are WEBDAV_USERNAME and
WEBDAV_PASSWORD, respectively.

In the future it will also be possible to read the credentials from a
configuration file.

## TODO

- maxconns doesn't work yet. this is complicated with the Go HTTP client.
- add configuration file
- timeout handling and interrupt handling
- we use busy-loop locking, yuck. use semaphores built on channels.
- rewrite fuse.go code to use the bazil/fuse abstraction instead of bazil/fuse/fs.  
  perhaps switch to  
  - https://github.com/hanwen/go-fuse
  - https://github.com/jacobsa/fuse

## Unix filesystem extensions for webdav.

Not ever going to happen, but if you wanted a more unix-like
experience and better performance, here are a few ideas:

- Content-Type: for unix pipes / chardevs / etc
- contentsize property (read-write)
- inodenumber property
- unix properties like uid/gid/mode
- DELETE Depth 0 for collections (no delete if non-empty)
- return updated PROPSTAT information after operations
  like PUT / DELETE / MKCOL / MOVE

