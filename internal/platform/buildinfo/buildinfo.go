// Package buildinfo contains values injected when command binaries are built.
package buildinfo

// Version is the application version. Release builds may replace it with
// -ldflags.
var Version = "dev"
