package sites

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPhotoCollisionIndex(t *testing.T) {
	files := map[string][]byte{"images/front.jpg": nil, "images/dir.jpg/child": nil}
	for i := 2; i <= 20000; i++ {
		files[fmt.Sprintf("images/front-%d.jpg", i)] = nil
	}
	occupied, err := photoOccupiedPaths(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	path, err := nextPhotoPath(context.Background(), occupied, "front.jpg")
	if err != nil || path != "images/front-20001.jpg" || !occupied["images/dir.jpg"] {
		t.Fatalf("collision: %s %v", path, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := photoOccupiedPaths(ctx, files); !errors.Is(err, context.Canceled) {
		t.Fatal("index ignored cancellation")
	}
	if _, err := nextPhotoPath(ctx, occupied, "front.jpg"); !errors.Is(err, context.Canceled) {
		t.Fatal("search ignored cancellation")
	}
}

func TestMatchesPhotoName(t *testing.T) {
	for _, tc := range []struct {
		path, name string
		want       bool
	}{
		{"images/front.jpg", "front.jpg", true}, {"images/front-2.jpg", "front.jpg", true},
		{"images/front-02.jpg", "front.jpg", false}, {"images/front-1.jpg", "front.jpg", false},
		{"images/other.jpg", "front.jpg", false}, {"other/front.jpg", "front.jpg", false},
		{"images/front-2.webp", "front.jpg", false},
		{"images/" + strings.Repeat("x", 58) + "-2.jpg", strings.Repeat("x", 60) + ".jpg", true},
	} {
		if got := matchesPhotoName(tc.path, tc.name); got != tc.want {
			t.Errorf("%s / %s: %v", tc.path, tc.name, got)
		}
	}
}
