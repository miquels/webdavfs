# HTTP PATCH support

This is a markdown translation of the document at
[http://sabre.io/dav/http-patch/](http://sabre.io/dav/http-patch/)
[© 2018 fruux GmbH](https://fruux.com/)

The `Sabre\\DAV\\PartialUpdate\\Plugin` from the Sabre DAV library provides
support for the HTTP PATCH method [RFC5789](http://tools.ietf.org/html/rfc5789).
This allows you to update just a portion of a file, or append to a file.

This document can be used as a spec for other implementers. There is some
DAV-specific stuff in this document, but only in relation to the OPTIONS
request.

## A sample request

```
PATCH /file.txt
Content-Length: 4
Content-Type: application/x-sabredav-partialupdate
X-Update-Range: bytes=3-6

ABCD
```

This request updates 'file.txt', specifically the bytes 3-6 (inclusive) to
`ABCD`.

If you just want to append to an existing file, use the following syntax:

```
PATCH /file.txt
Content-Length: 4
Content-Type: application/x-sabredav-partialupdate
X-Update-Range: append

1234
```

The last request adds 4 bytes to the bottom of the file.

## The rules

- The `Content-Length` header is required.
- `X-Update-Range` is also required.
- The `bytes` value is the exact same as the HTTP Range header. The two numbers
  are inclusive (so `3-6` means that bytes 3,4,5 and 6 will be updated).
- Just like the HTTP Range header, the specified bytes is a 0-based index.
- The `application/x-sabredav-partialupdate` must also be specified.
- The end-byte is optional.
- The start-byte cannot be omitted.
- If the start byte is negative, it's calculated from the end of the file. So
  `-1` will update the last byte in the file.
- Use `X-Update-Range: append` to add to the end of the file.
- Neither the start, nor the end-byte have to be within the file's current size.
- If the start-byte is beyond the file's current length, the space in between
  will be filled with NULL bytes (`0x00`).
- The specification currently does not support multiple ranges.
- If both start and end offsets are given, than both must be non-negative, and
  the end offset must be greater or equal to the start offset.

## More examples

The following table illustrates most types of requests and what the end-result
of them will be.

It is assumed that the input file contains `1234567890`, and the request body
always contains 4 dashes (`----`).

X-Update-Range header | Result
--------------------- | -------
`bytes=0-3`           | `----567890`
`bytes=1-4`           | `1----67890`
`bytes=0-`            | `----567890`
`bytes=-4`            | `123456----`
`bytes=-2`            | `12345678----`
`bytes=2-`            | `12----7890`
`bytes=12-`           | `1234567890..----`
`append`              | `1234567890----`

Please note that in the `bytes=12-` example, we used dots (`.`) to represent
what are actually `NULL` bytes (so `0x00`). The null byte is not printable.

## Status codes

### The following status codes should be used:

Status code | Reason
----------- | ------
200 or 204  | When the operation was successful
400         | Invalid `X-Update-Range` header
411         | `Content-Length` header was not provided
415         | Unrecognized content-type, should be `application/x-sabredav-partialupdate`
416         | If there was something wrong with the bytes, such as a `Content-Length` not matching with what was sent as the start and end bytes, or an end byte being lower than the start byte.

## OPTIONS

If you want to be compliant with SabreDAV's implementation of PATCH, you must
also return 'sabredav-partialupdate' in the 'DAV:' header:

```
HTTP/1.1 204 No Content
DAV: 1, 2, 3, sabredav-partialupdate, extended-mkcol
```

This is only required if you are adding this feature to a DAV server. For
non-webdav implementations such as REST services this is optional.

