package siteshttp

import (
	"errors"
	"net/url"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
)

// filterSiteList answers sites.FilterSiteList refusals as a 400 body.
func filterSiteList(list []model.Site, params url.Values) ([]model.Site, *httpx.ErrorResponse) {
	filtered, err := sites.FilterSiteList(list, params)
	var siteErr *sites.Error
	if errors.As(err, &siteErr) {
		return nil, &httpx.ErrorResponse{Error: siteErr.Message, Code: siteErr.Code}
	}
	return filtered, nil
}
