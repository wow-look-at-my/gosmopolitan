module cmd

go 1.27

require (
	github.com/google/pprof v0.0.0-20260507013755-92041b743c96
	github.com/klauspost/compress v1.20.0
	github.com/stretchr/testify v1.12.1
	github.com/wow-look-at-my/go-mmap v0.0.0
	github.com/wow-look-at-my/go-s3-server/cacheclient v0.0.0
	golang.org/x/arch v0.27.1-0.20260521044007-9c1a596a2c97
	golang.org/x/mod v0.41.0
	golang.org/x/sync v0.23.0
	golang.org/x/sys v0.48.0
	golang.org/x/telemetry v0.0.0-20260908163034-4bcc4b2ee518
	golang.org/x/term v0.43.0
	golang.org/x/tools v0.49.0
)

// The org's x/tools, which is also what the vendor tree's submodule holds. The
// fork declares the upstream module path, and a replacement is checked against
// the path being replaced rather than where it is fetched from, so the remap is
// legal. Both sides carry a version: vendor/modules.txt keys a replacement by
// the module version it annotates, and a wildcard has no version to match that
// key. The left one is the version the build list selects, because x/mod
// requires it, and the right one is the org placeholder for a branch head.
replace golang.org/x/tools v0.49.0 => github.com/wow-look-at-my/gosmopolitan_tools v0.0.0

require (
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/ianlancetaylor/demangle v0.0.0-20250417193237-f615e6bd150b // indirect
	github.com/pierrec/lz4/v4 v4.1.27 // indirect
	github.com/wow-look-at-my/go-containers v0.0.0 // indirect
	github.com/wow-look-at-my/go-ipc v0.0.0 // indirect
	github.com/wow-look-at-my/go-shm v0.0.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
)
