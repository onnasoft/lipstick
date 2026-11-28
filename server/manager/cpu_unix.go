//go:build unix

package manager

import "syscall"

// processCPUSeconds is the CPU time the process has used, user and system together.
func processCPUSeconds() float64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	user := float64(ru.Utime.Sec) + float64(ru.Utime.Usec)/1e6
	system := float64(ru.Stime.Sec) + float64(ru.Stime.Usec)/1e6
	return user + system
}
