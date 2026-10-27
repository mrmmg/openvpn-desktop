package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.yaml")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetActiveProfile("office.ovpn"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOverrides("office.ovpn", Overrides{RouteNoPull: true}); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.ActiveProfile() != "office.ovpn" || !s2.Overrides("office.ovpn").RouteNoPull {
		t.Fatalf("not persisted: %+v", s2.Snapshot())
	}
	if err := s2.SetOverrides("office.ovpn", Overrides{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Snapshot().Overrides["office.ovpn"]; ok {
		t.Fatal("zero overrides must be removed")
	}
	_ = s2.SetOverrides("office.ovpn", Overrides{BlockIPv6: true})
	if err := s2.ForgetProfile("office.ovpn"); err != nil {
		t.Fatal(err)
	}
	if s2.ActiveProfile() != "" || !s2.Overrides("office.ovpn").IsZero() {
		t.Fatal("forget did not clear state")
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("state mode %o", fi.Mode().Perm())
	}
}

func TestCorruptStateIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.yaml")
	_ = os.WriteFile(path, []byte("active_profile: [\n"), 0o600)
	s, err := Open(path)
	if err == nil || s == nil {
		t.Fatal("corrupt state must return a usable store and an error")
	}
	if err := s.SetActiveProfile("x"); err != nil {
		t.Fatal(err)
	}
}

func TestHistory(t *testing.T) {
	h := NewHistory(filepath.Join(t.TempDir(), "h.jsonl"))
	h.MaxEntries = 5
	now := time.Now()
	for i := 0; i < 12; i++ {
		e := HistoryEntry{Profile: "p", Started: now.Add(time.Duration(i) * time.Minute), Reason: "r"}
		if i%2 == 0 {
			e.Connected = e.Started.Add(time.Second)
			e.Ended = e.Connected.Add(time.Minute)
		}
		if err := h.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	list, err := h.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) > 12 || len(list) < 5 {
		t.Fatalf("len = %d", len(list))
	}
	if !list[0].Started.After(list[len(list)-1].Started) {
		t.Fatal("newest first")
	}
	if list[1].Duration() != time.Minute || list[0].Duration() != 0 {
		t.Fatalf("durations %v %v", list[1].Duration(), list[0].Duration())
	}
}
