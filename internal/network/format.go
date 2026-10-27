package network

import "fmt"

// FormatBytes renders a byte count with binary-ish units (KB = 1024 B).
func FormatBytes(n uint64) string {
	return formatUnits(float64(n), 1024, []string{"B", "KB", "MB", "GB", "TB", "PB"})
}

// FormatRate renders bytes/second as "B/s" or, with bits=true, as "bit/s".
func FormatRate(bytesPerSec float64, bits bool) string {
	if bits {
		return formatUnits(bytesPerSec*8, 1000, []string{"bit/s", "Kbit/s", "Mbit/s", "Gbit/s", "Tbit/s"})
	}
	return formatUnits(bytesPerSec, 1024, []string{"B/s", "KB/s", "MB/s", "GB/s", "TB/s"})
}

func formatUnits(v, base float64, units []string) string {
	if v < 0 {
		v = 0
	}
	i := 0
	for v >= base && i < len(units)-1 {
		v /= base
		i++
	}
	switch {
	case i == 0:
		return fmt.Sprintf("%.0f %s", v, units[i])
	case v >= 100:
		return fmt.Sprintf("%.0f %s", v, units[i])
	case v >= 10:
		return fmt.Sprintf("%.1f %s", v, units[i])
	default:
		return fmt.Sprintf("%.2f %s", v, units[i])
	}
}
