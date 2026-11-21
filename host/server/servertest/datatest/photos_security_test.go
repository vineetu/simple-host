package datatest

import (
	"os"
	"path/filepath"
	"testing"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/sites"
)

func TestPhotoAncestorConflicts(t *testing.T) {
	for _, path := range []string{"images", "images/front.jpg/child"} {
		t.Run(path, func(t *testing.T) {
			a := servertest.NewPersonApp(t, "off")
			p := a.NewPerson(t, "photoconflict")
			a.Deploy(t, p, "home")
			uid, _ := a.UserID(t, p)
			dest := filepath.Join(a.Sites.Disk.VersionDir(uid, "home", 1), path)
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dest, []byte("keep"), 0644); err != nil {
				t.Fatal(err)
			}
			body, ct := photoMultipart(t, []string{"front.jpg"}, [][]byte{photoJPEG(t, 8, 6)})
			r := a.At(t, "POST", "simple-host.test", "/v1/sites/home/photos", body, map[string]string{"X-API-Key": p.Key, "Content-Type": ct})
			if r.Status != 409 {
				t.Fatalf("conflict: %d %s", r.Status, r.Body)
			}
			if got, err := os.ReadFile(dest); err != nil || string(got) != "keep" {
				t.Fatal("conflicting file changed")
			}
		})
	}
}

func TestPhotoReservedNamesKeepExistingHosts(t *testing.T) {
	for _, handle := range []string{"photos", "photo-codecs"} {
		if accounts.CheckNewHandle(handle) == nil || !sites.HandleAddressable(handle) {
			t.Fatalf("claim/serve policy: %s", handle)
		}
		t.Run(handle, func(t *testing.T) {
			a, dir := servertest.NewSiteApp(t, "serve")
			a.Sites.Hosts.SetPersonHosts("serve")
			servertest.MarkReady(t, dir, handle)
			p := a.NewPerson(t, "oldphoto")
			uid, _ := a.UserID(t, p)
			if _, err := a.Database.Exec(`UPDATE users SET handle=$1 WHERE id=$2`, handle, uid); err != nil {
				t.Fatal(err)
			}
			a.Deploy(t, p, "home")
			for host, path := range map[string]string{handle + "." + servertest.PcSiteDomain: "/home/", "home." + handle + "." + servertest.PcSiteDomain: "/"} {
				r := a.At(t, "GET", host, path, nil, nil)
				if r.Status != 200 || string(r.Body) != "<h1>home</h1>" {
					t.Fatalf("existing host %s: %d", host, r.Status)
				}
			}
		})
	}
}
