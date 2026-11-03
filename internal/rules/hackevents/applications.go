package hackevents

// ApplicationsOpenNow is the applications_open rule shared by GET/POST
// .../apply, the directory and the public event JSON: the organiser's
// switch, the right stage, and not taken down (a draft or taken-down event
// never reaches this: callers 404 first). Was hackhttp's hack_apply.go
// hackApplicationsOpenNow; moved in cleanup 3.2 slice 4 since it is pure.
func ApplicationsOpenNow(applicationsOpen, listed bool, stage string, takenDown bool) bool {
	if takenDown || !applicationsOpen || !listed {
		return false
	}
	return stage == "open" || stage == "building"
}
