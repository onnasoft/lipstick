//go:build !unix

package manager

// processCPUSeconds is not available on this platform; it reports 0.
func processCPUSeconds() float64 { return 0 }
