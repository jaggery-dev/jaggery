//go:build unix

package inventory

import "golang.org/x/sys/unix"

// rootDisk reports the total and free bytes of the filesystem holding "/".
func rootDisk() (total, free uint64) {
	var st unix.Statfs_t
	if err := unix.Statfs("/", &st); err != nil {
		return 0, 0
	}
	bsize := uint64(st.Bsize)
	return st.Blocks * bsize, st.Bavail * bsize
}
