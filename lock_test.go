package main

import (
	"path/filepath"
	"testing"
)

func TestExclusiveLockAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot.lock")
	release, e := lockState(path)
	if e != nil {
		t.Fatal(e)
	}
	second, e := lockState(path)
	if e == nil {
		second()
		release()
		t.Fatal("second process lock was allowed")
	}
	release()
	third, e := lockState(path)
	if e != nil {
		t.Fatal("lock was not released", e)
	}
	third()
}
