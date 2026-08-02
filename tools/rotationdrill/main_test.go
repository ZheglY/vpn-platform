package main

import "testing"

func TestRotationDrill(t *testing.T) {
	if err := run(); err != nil {
		t.Fatal(err)
	}
}
