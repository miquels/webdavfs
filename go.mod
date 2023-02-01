module github.com/miquels/webdavfs

go 1.15

require (
	bazil.org/fuse v0.0.0-20200419173433-3ba628eaf417
	github.com/pborman/getopt/v2 v2.1.0
	github.com/yuin/goldmark v1.4.13 // indirect
	golang.org/x/mod v0.6.0-dev.0.20220419223038-86c51ed26bb4 // indirect
	golang.org/x/net v0.0.0-20210423184538-5f58ad60dda6
	golang.org/x/sync v0.0.0-20220722155255-886fb9371eb4 // indirect
	golang.org/x/sys v0.1.0 // indirect
	golang.org/x/term v0.1.0 // indirect
	golang.org/x/text v0.3.7 // indirect
)

replace bazil.org/fuse => bazil.org/fuse v0.0.0-20180421153158-65cc252bf669 // pin to latest version that supports macOS. see https://github.com/bazil/fuse/issues/224
replace golang.org/x/net => golang.org/x/net v0.0.0-20210423184538-5f58ad60dda6 // pin to version that still supports go 1.15 (Debian 11)
