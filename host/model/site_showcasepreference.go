package model

type ShowcasePreference struct {
	Pinned bool `json:"pinned"`
	Order  int  `json:"order"`
}
