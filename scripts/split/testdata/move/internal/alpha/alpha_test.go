package alpha

import (
	"os"
	"testing"
)

func TestData(t *testing.T) {
	if Data != "embedded\n" {
		t.Fatal(Data)
	}
	b, err := os.ReadFile("../../db/schema.sql")
	if err != nil || string(b) != "schema\n" {
		t.Fatal(string(b), err)
	}
}
