module github.com/miquels/webdavfs

go 1.15

require (
	bazil.org/fuse v0.0.0-20200419173433-3ba628eaf417
	github.com/pborman/getopt/v2 v2.1.0
	golang.org/x/net v0.0.0-20200324143707-d3edc9973b7e
	golang.org/x/sys v0.0.0-20200420163511-1957bb5e6d1f
)

replace bazil.org/fuse => bazil.org/fuse v0.0.0-20180421153158-65cc252bf669 // pin to latest version that supports macOS. see https://github.com/bazil/fuse/issues/224

replace golang.org/x/net => golang.org/x/net v0.0.0-20200324143707-d3edc9973b7e // pin to version that still supports go 1.15 (Debian 11)
replace golang.org/x/sys => golang.org/x/sys golang.org/x/sys v0.0.0-20200420163511-1957bb5e6d1f // pin to version that still supports go 1.15 (Debian 11)
