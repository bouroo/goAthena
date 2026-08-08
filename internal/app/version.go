package app

// Build metadata, overridden at release time via -ldflags "-X ...=.x=...". Defaults
// describe a local/dev build so `goathena version` is meaningful without a release.
var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)
