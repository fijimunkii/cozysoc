package storage

const filesystemPressureThresholdBytes int64 = 128 << 20

type FilesystemCapacityState string

const (
	FilesystemCapacityUnavailable FilesystemCapacityState = "unavailable"
	FilesystemCapacityCurrent     FilesystemCapacityState = "current"
	FilesystemCapacityPressure    FilesystemCapacityState = "pressure"
	FilesystemCapacityFull        FilesystemCapacityState = "full"
)

type FilesystemCapacity struct {
	Supported       bool
	State           FilesystemCapacityState
	TotalBytes      int64
	AvailableBytes  int64
	PressureAtBytes int64
}

func classifyFilesystemCapacity(supported bool, totalBytes, availableBytes int64) FilesystemCapacity {
	capacity := FilesystemCapacity{
		Supported:       supported,
		State:           FilesystemCapacityUnavailable,
		TotalBytes:      totalBytes,
		AvailableBytes:  availableBytes,
		PressureAtBytes: filesystemPressureThresholdBytes,
	}
	if !supported {
		return capacity
	}
	if totalBytes <= 0 || availableBytes < 0 || availableBytes > totalBytes {
		return capacity
	}
	switch {
	case availableBytes == 0:
		capacity.State = FilesystemCapacityFull
	case availableBytes <= filesystemPressureThresholdBytes:
		capacity.State = FilesystemCapacityPressure
	default:
		capacity.State = FilesystemCapacityCurrent
	}
	return capacity
}
