package hackevents

// ApplicationsOpenNow is the applications_open rule shared by GET/POST .../apply, the directory and the public event JSON.
func ApplicationsOpenNow(applicationsOpen, listed bool, stage string, takenDown bool) bool {
	if takenDown || !applicationsOpen || !listed {
		return false
	}
	return stage == "open" || stage == "building"
}
