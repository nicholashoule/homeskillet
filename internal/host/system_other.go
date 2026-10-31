//go:build !linux && !darwin && !windows

// Describing other systems, which homeskillet does not support: nothing is
// detected.

package host

// DescribeSystem returns nothing on systems homeskillet does not support.
func DescribeSystem() System {
	return System{}
}
