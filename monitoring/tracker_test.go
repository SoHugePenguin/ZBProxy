package monitoring

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/layou233/zbproxy/v3/adapter"
	"github.com/layou233/zbproxy/v3/common/bufio"
)

func metadata(name string) *adapter.Metadata {
	return &adapter.Metadata{ServiceName: "game", Minecraft: &adapter.MinecraftMetadata{NextState: 2, PlayerName: name}}
}

func TestSessionsMergeAndCheckpointWithoutDoubleCounting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "statistics.json")
	tracker, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first := tracker.Begin(metadata("Steve"), "old-route", "mc.example:25565")
	first.info.StartedAt = time.Now().Add(-time.Minute)
	first.AddUpload(25)
	first.download.Add(50)
	second := tracker.Begin(metadata("STEVE"), "new-route", "mc.example:25566")
	second.AddUpload(75)
	second.download.Add(150)
	for i := 0; i < 3; i++ {
		snapshot := tracker.Snapshot()
		if snapshot.Summary.ActiveSessions != 2 || snapshot.Summary.OnlinePlayers != 1 || snapshot.Summary.UploadBytes != 100 || snapshot.Summary.DownloadBytes != 200 || snapshot.Summary.DurationMS < 60000 || snapshot.Players[0].LastOutbound != "new-route" {
			t.Fatalf("bad active totals: %+v", snapshot)
		}
		if err := tracker.Save(); err != nil {
			t.Fatal(err)
		}
	}
	resumed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := resumed.Snapshot()
	if checkpoint.Summary.ActiveSessions != 0 || checkpoint.Summary.UploadBytes != 100 || checkpoint.Summary.Connections != 2 || checkpoint.Summary.DurationMS < 60000 {
		t.Fatalf("bad restart state: %+v", checkpoint)
	}
	second.End()
	first.End()
	first.End()
	if err := tracker.Save(); err != nil {
		t.Fatal(err)
	}
	snapshot := tracker.Snapshot()
	if snapshot.Summary.ActiveSessions != 0 || snapshot.Summary.UploadBytes != 100 || snapshot.Summary.DownloadBytes != 200 || snapshot.Summary.Connections != 2 || len(snapshot.Players) != 1 || snapshot.Players[0].LastOutbound != "new-route" {
		t.Fatalf("sessions counted twice or route changed: %+v", snapshot)
	}
}

type testConn struct {
	net.Conn
	reader io.Reader
}

func (c *testConn) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c *testConn) Write(p []byte) (int, error) { return len(p), nil }

func TestTrafficWrapperNotBypassedByCopy(t *testing.T) {
	tracker, _ := Open("")
	session := tracker.Begin(metadata("Alex"), "route", "mc.example:25565")
	conn := session.Wrap(&testConn{reader: bytes.NewReader(make([]byte, 20000))})
	if _, err := bufio.Copy(conn, bytes.NewReader(make([]byte, 12345))); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.Copy(io.Discard, conn); err != nil {
		t.Fatal(err)
	}
	session.End()
	summary := tracker.Snapshot().Summary
	if summary.UploadBytes != 12345 || summary.DownloadBytes != 20000 {
		t.Fatalf("copy bypassed accounting: %+v", summary)
	}
}

func TestConcurrentAccountingAndSnapshots(t *testing.T) {
	tracker, _ := Open("")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session := tracker.Begin(metadata("Alex"), "route", "mc.example:25565")
			for j := 0; j < 100; j++ {
				session.AddUpload(7)
				session.download.Add(13)
				tracker.Snapshot()
			}
			session.End()
		}()
	}
	wg.Wait()
	s := tracker.Snapshot().Summary
	if s.UploadBytes != 5600 || s.DownloadBytes != 10400 || s.Connections != 8 || s.ActiveSessions != 0 {
		t.Fatalf("lost counters: %+v", s)
	}
}

func TestStatusAndEmptyPlayersAreNotOnline(t *testing.T) {
	tracker, _ := Open("")
	m := metadata("Alex")
	m.Minecraft.NextState = 1
	if tracker.Begin(m, "route", "target") != nil || tracker.Begin(metadata(""), "route", "target") != nil {
		t.Fatal("status or empty player became an online session")
	}
}

func TestPersistenceFailureRecoveryAndFinalFlush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "stats.json")
	tracker, _ := Open(path)
	session := tracker.Begin(metadata("Alex"), "route", "target")
	session.AddUpload(10)
	if tracker.Save() == nil || tracker.Snapshot().PersistenceError == "" {
		t.Fatal("checkpoint failure not visible")
	}
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	stop := tracker.StartPersistence(context.Background(), func(err error) { t.Errorf("save failed: %v", err) })
	session.AddUpload(20)
	stop()
	loaded, err := Open(path)
	if err != nil || loaded.Snapshot().Summary.UploadBytes != 30 || tracker.Snapshot().PersistenceError != "" {
		t.Fatalf("final flush or recovery failed: %v", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("unexpected statistics file permissions: %v", info.Mode())
	}
}

type partialConn struct{ net.Conn }

func (c *partialConn) Write(p []byte) (int, error) { return 2, io.ErrClosedPipe }
func (c *partialConn) Read(p []byte) (int, error) {
	copy(p, "abc")
	return 3, io.EOF
}

func TestPartialTrafficCountsActualBytes(t *testing.T) {
	tracker, _ := Open("")
	session := tracker.Begin(metadata("Alex"), "route", "target")
	conn := session.Wrap(&partialConn{})
	if _, err := conn.Write(make([]byte, 100)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	conn.Read(make([]byte, 100))
	session.End()
	s := tracker.Snapshot().Summary
	if s.UploadBytes != 2 || s.DownloadBytes != 3 {
		t.Fatalf("counted requested instead of actual bytes: %+v", s)
	}
}
