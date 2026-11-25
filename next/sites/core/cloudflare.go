package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Cloudflare is the zone in front of the site addresses (CF_ZONE_ID,
// CF_API_TOKEN). Nil when it is not set: nothing is purged.
type Cloudflare struct {
	URL, Zone, Token string // URL: the API, https://api.cloudflare.com/client/v4
	Client           *http.Client
}

// purge drops what Cloudflare keeps for the owner's public page and the
// given sites (none given: every site of the owner), in the background,
// tried 4 times; a final failure is one log line. The edge keeps a page an
// hour at most, so a missed purge heals by itself.
func (s *Sites) purge(ownerID string, siteIDs []string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if len(siteIDs) == 0 {
		rows, err := s.DB.QueryContext(ctx, `SELECT id FROM sites WHERE owner_id = $1`, ownerID)
		if err == nil {
			for rows.Next() {
				var id string
				rows.Scan(&id)
				siteIDs = append(siteIDs, id)
			}
			rows.Close()
		}
	}
	tags := []string{"owner:" + ownerID}
	for _, id := range siteIDs {
		tags = append(tags, "site:"+id)
	}
	for len(tags) > 0 { // one call, unless an owner-wide change names more tags than a call takes
		n := min(len(tags), 30)
		body, _ := json.Marshal(map[string][]string{"tags": tags[:n]})
		var err error
		for try := 0; try < 4; try++ {
			if try > 0 {
				time.Sleep(2 * time.Second)
			}
			if err = s.Cloudflare.post(ctx, body); err == nil {
				break
			}
		}
		if err != nil {
			log.Printf("cloudflare purge %v: %v", tags[:n], err)
		}
		tags = tags[n:]
	}
}

func (c *Cloudflare) post(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, "POST", c.URL+"/zones/"+c.Zone+"/purge_cache", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	return nil
}
