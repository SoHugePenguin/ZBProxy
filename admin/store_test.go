package admin

import (
	"fmt"
	"os"
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
	if _, err := s.UpdatePlayers(names, nil); err != nil {
		t.Fatal(err)
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
	if _, err := s.UpdatePlayers([]string{"Steve"}, nil); err != nil {
		t.Fatal(err)
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
		if _, err := s.UpdatePlayers([]string{fmt.Sprintf("Player%d", i)}, nil); err != nil {
			t.Fatal(err)
		}
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
	if _, err = s.UpdatePlayers([]string{"steve"}, nil); err != nil {
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

func TestRegistrationAndWhitelistAreIndependent(t *testing.T) {
	s := openTestStore(t)
	on := true
	if _, err := s.Update(&on, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdatePlayers([]string{"Steve", "Alex", "STEVE"}, nil); err != nil {
		t.Fatal(err)
	}
	if s.ListPlayers("", 1).Total != 2 || s.List("", 1).Total != 0 || s.AllowPlayer("Steve") {
		t.Fatal("registration granted access")
	}
	if _, err := s.Update(nil, []string{"Steve", "Visitor"}, nil); err == nil {
		t.Fatal("unregistered player accepted")
	}
	if s.List("", 1).Total != 0 {
		t.Fatal("invalid batch partially committed")
	}
	if _, err := s.Update(nil, []string{"sTEVe"}, nil); err != nil {
		t.Fatal(err)
	}
	if !s.AllowPlayer("STEVE") || s.AllowPlayer("Alex") {
		t.Fatal("wrong whitelist membership")
	}
	if _, err := s.Update(nil, nil, []string{"Steve"}); err != nil {
		t.Fatal(err)
	}
	if s.ListPlayers("", 1).Total != 2 || s.AllowPlayer("Steve") {
		t.Fatal("whitelist removal affected registration")
	}
	if _, err := s.Update(nil, []string{"Alex"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdatePlayers(nil, []string{"ALEX"}); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.ListPlayers("", 1).Total != 1 || reopened.List("", 1).Total != 0 || reopened.AllowPlayer("Alex") {
		t.Fatal("registration removal failed to revoke access after restart")
	}
}

func TestLegacyWhitelistMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "players.json")
	if err := os.WriteFile(path, []byte(`{"enabled":true,"players":["Steve","Alex"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.ListPlayers("", 1).Total != 2 || s.List("", 1).Total != 2 || !s.AllowPlayer("STEVE") || s.AllowPlayer("Visitor") {
		t.Fatal("migration changed access")
	}
	if _, err = s.UpdatePlayers([]string{"Visitor"}, nil); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.ListPlayers("", 1).Total != 3 || reopened.List("", 1).Total != 2 || reopened.AllowPlayer("Visitor") {
		t.Fatal("split lists were lost on restart")
	}
}
