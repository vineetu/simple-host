package storage

import (
	"github.com/vsriram/simple-host/internal/model"
)

type AccessMatrix = model.AccessMatrix
type Preset = model.Preset

func PresetMatrix(id string) (AccessMatrix, bool) { return model.PresetMatrix(id) }
func PresetName(m AccessMatrix) string            { return model.PresetName(m) }
func Narrower(a, b string) string                 { return model.Narrower(a, b) }
func TranslateOld(read, write, mode string) (AccessMatrix, string, string) {
	return model.TranslateOld(read, write, mode)
}
func LegacyMatrix(read, write, mode string) AccessMatrix {
	return model.LegacyMatrix(read, write, mode)
}
func OldFields(m AccessMatrix) (read, write, mode string, ok bool) { return model.OldFields(m) }

type TableRule = model.TableRule
type AccessRequest = model.AccessRequest

func ValidPolicy(p string) bool { return model.ValidPolicy(p) }

type Resource = model.Resource

var Presets = model.Presets

const WhoNobody = model.WhoNobody
const WhoOwner = model.WhoOwner
const WhoOwn = model.WhoOwn
const WhoSignedIn = model.WhoSignedIn
const WhoAnyone = model.WhoAnyone
const WhoChecked = model.WhoChecked
