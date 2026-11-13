package admin

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "whitelist.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStorePersistenceAndNoPlayerQuota(t *testing.T) {
	s := openTestStore(t)
	if !s.AllowPlayer("Visitor") {
		t.Fatal("new whitelist must be disabled")
	}
	names := make([]string, 10001)
	for i := range names {
		names[i] = fmt.Sprintf("Player_%05d", i)
	}
	n, err := s.Update(nil, names, nil)
	if err != nil || n != len(names) {
		t.Fatalf("add 10001 players: %d, %v", n, err)
	}
	on := true
	if _, err = s.Update(&on, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !s.AllowPlayer("pLaYeR_10000") || s.AllowPlayer("Visitor") {
		t.Fatal("case-insensitive whitelist check failed")
	}
	if n, err = s.Update(nil, []string{"PLAYER_10000"}, nil); err != nil || n != 0 {
		t.Fatalf("duplicate: %d, %v", n, err)
	}
	reopened, err := OpenStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.AllowPlayer("player_10000") || reopened.AllowPlayer("Visitor") {
		t.Fatal("whitelist lost after restart")
	}
	page := reopened.List("player_", 9999999)
	if page.Total != 10001 || page.Page != 201 || len(page.Players) != 1 {
		t.Fatalf("pagination: %+v", page)
	}
	page = reopened.List("player_10000", 999)
	if page.Page != 1 || page.Matched != 1 || len(page.Players) != 1 {
		t.Fatalf("search: %+v", page)
	}
	if _, err = reopened.Update(nil, nil, []string{"PLAYER_10000"}); err != nil {
		t.Fatal(err)
	}
	if reopened.AllowPlayer("player_10000") {
		t.Fatal("removed player still allowed")
	}
}

func TestFailedSaveAndInvalidInputLeavePolicyUnchanged(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Update(nil, []string{"Steve", "invalid-name"}, nil); err == nil {
		t.Fatal("accepted invalid name")
	}
	if s.List("", 1).Total != 0 {
		t.Fatal("partially applied invalid batch")
	}
	s.path = filepath.Join(t.TempDir(), "missing", "whitelist.json")
	on := true
	if _, err := s.Update(&on, []string{"Steve"}, nil); err == nil {
		t.Fatal("expected disk failure")
	}
	if !s.AllowPlayer("Visitor") || s.List("", 1).Total != 0 {
		t.Fatal("failed write changed live policy")
	}
}

func TestConcurrentChecksAndUpdates(t *testing.T) {
	s := openTestStore(t)
	var readers sync.WaitGroup
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for j := 0; j < 1000; j++ {
				s.AllowPlayer("Steve")
				s.List("", 1)
			}
		}()
	}
	for i := 0; i < 10; i++ {
		on := i%2 == 0
		if _, err := s.Update(&on, []string{fmt.Sprintf("Player%d", i)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	readers.Wait()
}

func BenchmarkWhitelistCheck(b *testing.B) {
	s, err := OpenStore(filepath.Join(b.TempDir(), "whitelist.json"))
	if err != nil {
		b.Fatal(err)
	}
	on := true
	if _, err = s.Update(&on, []string{"steve"}, nil); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !s.AllowPlayer("steve") {
			b.Fatal("unexpected rejection")
		}
	}
}
