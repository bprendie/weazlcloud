// Restic exposes its repository writer as internal packages. Keep this small
// adapter within that import namespace and pin the exact storage implementation.
module github.com/restic/restic/weazlcloud-writer

go 1.25.0

require github.com/restic/restic v0.18.0

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/elithrar/simple-scrypt v1.3.0 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/peterbourgon/unixtransport v0.0.4 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pkg/xattr v0.4.10 // indirect
	github.com/restic/chunker v0.4.0 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/net v0.37.0 // indirect
	golang.org/x/sync v0.12.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	golang.org/x/time v0.11.0 // indirect
)
