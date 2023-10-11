module github.com/miquels/webdavfs

go 1.12

require (
	bazil.org/fuse v0.0.0-20200419173433-3ba628eaf417
	github.com/pborman/getopt/v2 v2.1.0
	golang.org/x/net v0.17.0
)

replace bazil.org/fuse => bazil.org/fuse v0.0.0-20180421153158-65cc252bf669 // pin to latest version that supports macOS. see https://github.com/bazil/fuse/issues/224
