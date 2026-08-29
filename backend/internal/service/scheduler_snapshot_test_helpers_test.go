//go:build unit

package service

func schedulerTestCanonicalBucketCount() int {
	return len(schedulerCanonicalBuckets(0))
}

func schedulerTestAccountQueryCount() int {
	count := 0
	for _, platform := range schedulerSnapshotPlatforms() {
		count++
		if platform == PlatformAnthropic || platform == PlatformGemini {
			count++
		}
	}
	return count
}
