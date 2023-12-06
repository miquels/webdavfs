
# Webdav and HTTP requirements for WebdavFS.

## for readonly purposes

This works with most, if not all webdav servers. The minimum we need is:

- PROPFIND support (all webdav servers have this)
- The server needs to send the "Dav: 1,2" header to indicate webdav support
- GET Range: support (all servers should have this)

## For directory operations

Renaming files, creating directories, removing files/directories

- MOVE support
  * the implementation SHOULD honour the "Overwrite: T/F" header. This
    is used when MOVEing directories. Renaming a directory to another
    directory should not (recursively!!) delete the target directory if
    it exists. We check for this on the client side but preventing the
    server from doing this is a good thing.
- MKCOL support
- DELETE support

## For writing files.

Writing files is a delicate operation, we should take care to do it
correctly. Right now, the driver checks if we're talking to an Apache
or SabreDAV implementation because they are the only ones that implement
partial put.

- If-Match: * / If-None-Match: * support (RFC2616).  
  If-Match: * is used with the PUT method to prevent files being written
  when they don't exist anymore (for example removed on the server side).  
  If-None-Match: * is used to open a file exclusively (O_EXCL) and fail
  if it already exists.  
  Though this is very basic, there are servers that do not implement this,
  or not correctly.
- Partial PUT support.  
  This means writing just a part of a file, updating it in-place, instead
  of replacing an existing file. webdavFS detects what webserver it is
  talking  to. If it's Apache it uses PUT + Content-Range, if it's
  SabreDAV it uses PATCH + X-Update-Range. For more info, see:  
  https://blog.sphere.chronosempire.org.uk/2012/11/21/webdav-and-the-http-patch-nightmare  
  http://sabre.io/dav/http-patch/  

## Partial PUT support as a standard

There seems to be some movement in this space. RFC9110 mentions
[partial PUT using the `Content-Range` header](https://www.rfc-editor.org/rfc/rfc9110.html#name-partial-put), even though that is very unsafe - on servers not supporting it you'll get instant data corruption.

Separately, there's a [Byte Range PATCH](https://datatracker.ietf.org/doc/draft-wright-http-patch-byterange/) draft RFC. This one looks better, let's hope it goes forward.
