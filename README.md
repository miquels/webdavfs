# davfs3

## A FUSE filesystem for WEBDAV shares.

Most filesystem drivers for Webdav shares act somewhat like a mirror;
if a file is read then it's cached in its entirety on a local
drive, then read from there. Writing files is similar; if it's
just an update, the whole file is first written then sent back
to the webdav server. In many cases that is not optimal.

This filesystem driver behaves like a network filesystem. It doesn't
cache anything locally, it just sends out reads/writes over the
network.

For that to work, you need partial write support- and unfortunately,
there is no standard for that. See
https://blog.sphere.chronosempire.org.uk/2012/11/21/webdav-and-the-http-patch-nightmare

There is support in Apache and SabreDav for partial writes, so we
detect if it's Apache or SabreDav we're talking to and then use
their specific way to partially update files.

## What is working

Basic filesystem operations.

- files: create/delete/read/write/truncate/seek
- directories: mkdir rmdir readdir

## What is not yet working

- locking

## What will not ever work

- change permissions (all files are 644, all dirs are 755)
- change user/group
- devices / fifos / chardev / blockdev etc
- query filesystem size (df / vfsstat)
- truncate(2) / ftruncate(2) for any other length than "0"

This is basically because these are mostly just missing properties
from webdav.

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

