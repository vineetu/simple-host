package handler

import "time"

// Allow the full body at 250,000 bytes/s plus ten minutes for overhead and
// variation, while retaining a finite deadline for stalled requests.
func bodyReadTimeout(allowedBytes int64) time.Duration {
	const bytesPerSecond = 250000
	transfer := time.Duration(allowedBytes/bytesPerSecond)*time.Second +
		time.Duration(allowedBytes%bytesPerSecond)*time.Second/bytesPerSecond
	return max(15*time.Minute, transfer+10*time.Minute)
}
