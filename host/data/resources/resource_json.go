package resources

import (
	"context"

	"github.com/vsriram/simple-host/host/data"
)

// ResourceListJSON answers GET .../storage/resources: every resource of the
// site as the owner's tools see it.
func ResourceListJSON(s *data.Service, ctx context.Context, call data.Call) ([]map[string]any, error) {
	list, err := ListStorageResources(s, ctx, call)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		out = append(out, data.ResourceRulesJSON(item.Resource, item.RulesErrors))
	}
	return out, nil
}

// SaveResource answers PUT .../storage/resources/{name}: body is the
// request's configuration (DecodeResourceInput), saved by
// PutStorageResource. The answer is the resource as the owner's tools see
// it, with any warnings; created reports a new resource.
func SaveResource(s *data.Service, ctx context.Context, call data.Call, name string, body []byte) (map[string]any, bool, error) {
	in, err := DecodeResourceInput(body)
	if err != nil {
		return nil, false, err
	}
	in.Call, in.Name = call, name
	result, err := PutStorageResource(s, ctx, in)
	if err != nil {
		return nil, false, err
	}
	out := data.ResourceRulesJSON(result.Resource, result.RulesErrors)
	if len(result.Warnings) > 0 {
		out["warnings"] = result.Warnings
	}
	return out, result.Created, nil
}
