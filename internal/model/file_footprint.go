package model

// FileFootprint counts deploy files only: current and vN trees. Backend
// resources, markers and saved data have separate allowances.
type FileFootprint struct {
	Live     int64
	Versions map[int]int64
}

func (f FileFootprint) Total() int64 {
	n := f.Live
	for _, b := range f.Versions {
		n += b
	}
	return n
}
